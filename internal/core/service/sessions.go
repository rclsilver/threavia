package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"unicode"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"

	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/events"
	"github.com/rclsilver/threavia/internal/core/storage/postgres"
)

// titleLimit is how much of the first message becomes the generated Session
// title. The title has no technical meaning and the user can rename it.
const titleLimit = 60

// StartSessionInput is the first send of a client-side draft. Nothing exists in
// Core until this arrives (spec section 3.3).
type StartSessionInput struct {
	ProjectID          domain.ProjectID
	BackendInstanceID  domain.BackendInstanceID
	WorkingDirectoryID *domain.KnownDirectoryID
	Message            string
	// Internal delegation metadata, never accepted from the HTTP start route.
	ManagerSessionID *domain.SessionID
	Title            string
	ExecutionPolicy  *domain.ExecutionPolicy
	// NativeSessionID adopts a provider session that already exists on the
	// backend, typically one started in a terminal. Empty opens a fresh one.
	NativeSessionID string
	// IdempotencyKey makes a retried first send return the same Session rather
	// than creating a second one.
	IdempotencyKey string
}

// StartSessionResult is what the first send created.
type StartSessionResult struct {
	Session domain.Session `json:"session"`
	Run     domain.Run     `json:"run"`
	Job     domain.Job     `json:"job"`
}

// StartSession is the atomic first-send operation: it creates the Session, its
// first Run, its first Job and the first user message in a single transaction,
// so no empty or partially-created Session can ever exist.
func (s *Service) StartSession(ctx context.Context, identity auth.Identity, in StartSessionInput) (StartSessionResult, error) {
	message := strings.TrimSpace(in.Message)
	if message == "" {
		return StartSessionResult{}, fmt.Errorf("%w: the first message cannot be empty", ErrInvalid)
	}
	native, err := adoptedSession(in.NativeSessionID)
	if err != nil {
		return StartSessionResult{}, err
	}

	if in.IdempotencyKey != "" {
		if result, found, err := s.replayStartSession(ctx, identity, in.IdempotencyKey); err != nil {
			return StartSessionResult{}, err
		} else if found {
			return result, nil
		}
	}

	project, err := s.store.GetProject(ctx, identity.UserID, in.ProjectID)
	if err != nil {
		return StartSessionResult{}, translate(err)
	}
	if project.Status != domain.ProjectActive {
		return StartSessionResult{}, fmt.Errorf("%w: the project is archived", ErrConflict)
	}
	if err := s.checkBackendUsable(ctx, identity, in.BackendInstanceID); err != nil {
		return StartSessionResult{}, err
	}
	if err := s.checkWorkingDirectory(ctx, identity, in.ProjectID, in.BackendInstanceID, in.WorkingDirectoryID); err != nil {
		return StartSessionResult{}, err
	}

	result := StartSessionResult{
		Session: domain.Session{
			ID:                 domain.NewSessionID(),
			ProjectID:          in.ProjectID,
			Title:              deriveTitle(message),
			Status:             domain.SessionActive,
			WorkingDirectoryID: workingDirectoryString(in.WorkingDirectoryID),
			ManagerSessionID:   in.ManagerSessionID,
		},
	}
	if title := strings.TrimSpace(in.Title); title != "" {
		result.Session.Title = title
	}
	result.Run = domain.Run{
		ID:                domain.NewRunID(),
		SessionID:         result.Session.ID,
		BackendInstanceID: in.BackendInstanceID,
		NativeSessionID:   native,
		ResumeStatus:      domain.ResumeUnknown,
	}
	result.Job = domain.Job{
		ID:             domain.NewJobID(),
		RunID:          result.Run.ID,
		Status:         domain.JobQueued,
		IdempotencyKey: optionalString(in.IdempotencyKey),
		OriginChannel:  domain.ChannelFrom(ctx),
	}

	scope := domain.Scope{
		ProjectID: in.ProjectID,
		SessionID: result.Session.ID,
		RunID:     result.Run.ID,
		JobID:     result.Job.ID,
	}
	b := &batch{ownerID: identity.UserID}

	err = s.store.WithTx(ctx, func(tx *postgres.Store) error {
		if err := tx.CreateSession(ctx, &result.Session); err != nil {
			return err
		}
		if in.ExecutionPolicy != nil {
			if err := tx.SetSessionExecutionPolicy(ctx, identity.UserID, result.Session.ID, in.ExecutionPolicy); err != nil {
				return err
			}
		}
		if err := tx.CreateRun(ctx, &result.Run); err != nil {
			return err
		}
		if err := tx.CreateJob(ctx, &result.Job); err != nil {
			return err
		}
		return s.appendAll(ctx, tx, b,
			record{events.TypeSessionCreated, domain.Scope{ProjectID: scope.ProjectID, SessionID: scope.SessionID},
				SessionCreatedPayload{Title: result.Session.Title, WorkingDirectoryID: result.Session.WorkingDirectoryID, ManagerSessionID: in.ManagerSessionID}},
			record{events.TypeRunCreated, domain.Scope{ProjectID: scope.ProjectID, SessionID: scope.SessionID, RunID: scope.RunID},
				RunCreatedPayload{BackendInstanceID: string(in.BackendInstanceID)}},
			record{events.TypeJobCreated, scope, JobCreatedPayload{RunID: string(scope.RunID)}},
			record{events.TypeUserMessage, scope, UserMessagePayload{Text: message, ActorJobID: managerJobFrom(ctx)}},
		)
	})
	if err != nil {
		return StartSessionResult{}, translate(err)
	}

	s.flush(b)
	s.DispatchJob(ctx, result.Job.ID)
	return result, nil
}

// replayStartSession returns the Session a retried first send already created.
func (s *Service) replayStartSession(ctx context.Context, identity auth.Identity, key string) (StartSessionResult, bool, error) {
	job, err := s.store.JobByIdempotencyKey(ctx, identity.UserID, key)
	if errors.Is(err, postgres.ErrNotFound) {
		return StartSessionResult{}, false, nil
	}
	if err != nil {
		return StartSessionResult{}, false, translate(err)
	}

	run, err := s.store.GetRun(ctx, identity.UserID, job.RunID)
	if err != nil {
		return StartSessionResult{}, false, translate(err)
	}
	session, err := s.store.GetSession(ctx, identity.UserID, run.SessionID)
	if err != nil {
		return StartSessionResult{}, false, translate(err)
	}
	return StartSessionResult{Session: session, Run: run, Job: job}, true, nil
}

// PostMessage appends a message to an existing Session. It becomes a new Job on
// the current Run, which is how a second message resumes the same provider
// native session.
func (s *Service) PostMessage(ctx context.Context, identity auth.Identity, sessionID domain.SessionID, message, idempotencyKey string, delivery Delivery) (domain.Job, error) {
	message = strings.TrimSpace(message)
	if message == "" {
		return domain.Job{}, fmt.Errorf("%w: the message cannot be empty", ErrInvalid)
	}
	switch delivery {
	case "", DeliveryQueue:
	case DeliveryNow, DeliveryNext:
		return s.deliverToRunningJob(ctx, identity, sessionID, message, delivery)
	default:
		return domain.Job{}, fmt.Errorf("%w: unknown delivery %q", ErrInvalid, delivery)
	}
	return s.queueMessage(ctx, identity, sessionID, message, idempotencyKey, "")
}

// queueMessage makes a message a Job of its own on the current Run. A
// scheduled one carries the Schedule that sent it, so the timeline says it was
// not typed by anyone.
func (s *Service) queueMessage(ctx context.Context, identity auth.Identity, sessionID domain.SessionID, message, idempotencyKey string, scheduleID domain.ScheduleID) (domain.Job, error) {
	if idempotencyKey != "" {
		// Keys are per user, so a Job found here is already the caller's. It
		// is a replay only if it was made in the Session this request names: the
		// same key on another Session is a client bug, and answering it with a
		// Job from elsewhere would drop the message without a word.
		job, err := s.store.JobByIdempotencyKey(ctx, identity.UserID, idempotencyKey)
		if err == nil {
			run, err := s.store.GetRun(ctx, identity.UserID, job.RunID)
			if err != nil {
				return domain.Job{}, translate(err)
			}
			if run.SessionID != sessionID {
				return domain.Job{}, fmt.Errorf("%w: this Idempotency-Key was already used for a message in another session", ErrConflict)
			}
			return job, nil
		}
		if !errors.Is(err, postgres.ErrNotFound) {
			return domain.Job{}, translate(err)
		}
	}

	session, err := s.store.GetSession(ctx, identity.UserID, sessionID)
	if err != nil {
		return domain.Job{}, translate(err)
	}
	if session.Status != domain.SessionActive {
		return domain.Job{}, fmt.Errorf("%w: the session is archived", ErrConflict)
	}

	run, err := s.store.LatestRun(ctx, sessionID)
	if err != nil {
		return domain.Job{}, translate(err)
	}

	job := domain.Job{
		ID:             domain.NewJobID(),
		RunID:          run.ID,
		Status:         domain.JobQueued,
		IdempotencyKey: optionalString(idempotencyKey),
		OriginChannel:  domain.ChannelFrom(ctx),
	}
	scope := domain.Scope{ProjectID: session.ProjectID, SessionID: sessionID, RunID: run.ID, JobID: job.ID}
	b := &batch{ownerID: identity.UserID}

	err = s.store.WithTx(ctx, func(tx *postgres.Store) error {
		if err := tx.CreateJob(ctx, &job); err != nil {
			return err
		}
		if err := tx.TouchSession(ctx, sessionID); err != nil {
			return err
		}
		return s.appendAll(ctx, tx, b,
			record{events.TypeJobCreated, scope, JobCreatedPayload{RunID: string(run.ID)}},
			record{events.TypeUserMessage, scope, UserMessagePayload{Text: message, ScheduleID: scheduleID, ActorJobID: managerJobFrom(ctx)}},
		)
	})
	if err != nil {
		return domain.Job{}, translate(err)
	}

	s.flush(b)
	s.DispatchJob(ctx, job.ID)
	return job, nil
}

// Delivery is how a message reaches a Session that is already working (spec
// section 3.5).
type Delivery string

const (
	// DeliveryQueue makes the message a Job of its own, started once the
	// running one ends. The default, and the only one every backend supports.
	DeliveryQueue Delivery = "QUEUE"
	// DeliveryNow interrupts the running Job and reorients it at once.
	DeliveryNow Delivery = "NOW"
	// DeliveryNext reaches the running Job at its next step.
	DeliveryNext Delivery = "NEXT"
)

// deliverToRunningJob hands a message to the Job already running in a
// Session, rather than queueing a new one behind it.
//
// The message joins that Job's timeline: it is part of the work under way,
// and the answer to it comes in the same turn or the one the interruption
// starts. Refused, rather than quietly queued, when nothing is running or the
// backend cannot do it: the person chose to reach the work now, and turning
// that into "later" without saying so would answer a question they did not
// ask.
func (s *Service) deliverToRunningJob(ctx context.Context, identity auth.Identity, sessionID domain.SessionID, message string, delivery Delivery) (domain.Job, error) {
	session, err := s.store.GetSession(ctx, identity.UserID, sessionID)
	if err != nil {
		return domain.Job{}, translate(err)
	}
	if session.Status != domain.SessionActive {
		return domain.Job{}, fmt.Errorf("%w: the session is archived", ErrConflict)
	}
	run, err := s.store.LatestRun(ctx, sessionID)
	if err != nil {
		return domain.Job{}, translate(err)
	}
	job, err := s.store.ActiveJob(ctx, run.ID)
	if errors.Is(err, postgres.ErrNotFound) {
		return domain.Job{}, fmt.Errorf("%w: no job is running to receive it; send it as a new message", ErrConflict)
	}
	if err != nil {
		return domain.Job{}, translate(err)
	}
	switch job.Status {
	case domain.JobCancelling, domain.JobWaitingBackend:
		return domain.Job{}, fmt.Errorf("%w: the running job cannot take a message while it is %s",
			ErrConflict, strings.ToLower(string(job.Status)))
	}

	feature := domain.FeatureJobInputNext
	if delivery == DeliveryNow {
		feature = domain.FeatureJobInputNow
	}
	instance, err := s.store.BackendInstanceByID(ctx, run.BackendInstanceID)
	if err != nil {
		return domain.Job{}, translate(err)
	}
	if !instance.HasFeature(feature) {
		return domain.Job{}, fmt.Errorf("%w: the backend of this session cannot take a message while it works", ErrConflict)
	}
	conn, ok := s.backends.Lookup(run.BackendInstanceID)
	if !ok {
		return domain.Job{}, fmt.Errorf("%w: the backend of this session is offline", ErrConflict)
	}

	scope := domain.Scope{ProjectID: session.ProjectID, SessionID: sessionID, RunID: run.ID, JobID: job.ID}
	b := &batch{ownerID: identity.UserID}
	err = s.store.WithTx(ctx, func(tx *postgres.Store) error {
		if err := tx.TouchSession(ctx, sessionID); err != nil {
			return err
		}
		if delivery == DeliveryNow {
			// The turn that asked is the one being interrupted: what it was
			// waiting on no longer needs an answer.
			if err := tx.AbandonJobAttention(ctx, job.ID, "interrupted by a new message"); err != nil {
				return err
			}
			if err := transitionTo(ctx, tx, job.ID, domain.JobRunning, nil); err != nil {
				return err
			}
		}
		return s.appendAll(ctx, tx, b,
			record{events.TypeUserMessage, scope, UserMessagePayload{Text: message, Delivery: delivery, ActorJobID: managerJobFrom(ctx)}},
		)
	})
	if err != nil {
		return domain.Job{}, translate(err)
	}
	s.flush(b)

	command := &backendv1.CoreToBackend{CommandId: domain.NewUUID()}
	if delivery == DeliveryNow {
		command.Message = &backendv1.CoreToBackend_JobInputNow{JobInputNow: &backendv1.JobInputNow{
			RunId: string(run.ID), JobId: string(job.ID), Text: agentPrompt(managerJobFrom(ctx), message),
		}}
	} else {
		command.Message = &backendv1.CoreToBackend_JobInputNext{JobInputNext: &backendv1.JobInputNext{
			RunId: string(run.ID), JobId: string(job.ID), Text: agentPrompt(managerJobFrom(ctx), message),
		}}
	}
	conn.Send(command)

	job, err = s.store.JobByID(ctx, job.ID)
	return job, translate(err)
}

// RenameSession sets a user-chosen title.
func (s *Service) RenameSession(ctx context.Context, identity auth.Identity, sessionID domain.SessionID, title string) (domain.Session, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return domain.Session{}, fmt.Errorf("%w: the title cannot be empty", ErrInvalid)
	}

	session, err := s.store.RenameSession(ctx, identity.UserID, sessionID, title)
	if err != nil {
		return domain.Session{}, translate(err)
	}
	s.emit(ctx, identity.UserID, events.TypeSessionRenamed,
		domain.Scope{ProjectID: session.ProjectID, SessionID: session.ID},
		SessionRenamedPayload{Title: title})
	return session, nil
}

// SetSessionPinned pins a Session, to be reached from any Project, or unpins
// it. Every client of the person follows, since a pin is a shortcut they
// expect to find on the other device too.
func (s *Service) SetSessionPinned(ctx context.Context, identity auth.Identity, sessionID domain.SessionID, pinned bool) (domain.Session, error) {
	session, err := s.store.SetSessionPinned(ctx, identity.UserID, sessionID, pinned)
	if err != nil {
		return domain.Session{}, translate(err)
	}
	s.emit(ctx, identity.UserID, events.TypeSessionPinned,
		domain.Scope{ProjectID: session.ProjectID, SessionID: session.ID},
		SessionPinnedPayload{Pinned: pinned})
	return session, nil
}

// PinnedSessions lists the Sessions the person pinned, across Projects.
func (s *Service) PinnedSessions(ctx context.Context, identity auth.Identity) ([]domain.Session, error) {
	sessions, err := s.store.PinnedSessions(ctx, identity.UserID)
	return sessions, translate(err)
}

// SetSessionStatus archives or restores a Session.
func (s *Service) SetSessionStatus(ctx context.Context, identity auth.Identity, sessionID domain.SessionID, status domain.SessionStatus) (domain.Session, error) {
	current, err := s.store.GetSession(ctx, identity.UserID, sessionID)
	if err != nil {
		return domain.Session{}, translate(err)
	}
	if _, err := current.Status.Transition(status); err != nil {
		return domain.Session{}, fmt.Errorf("%w: %s", ErrConflict, err)
	}

	session, err := s.store.SetSessionStatus(ctx, identity.UserID, sessionID, status)
	if err != nil {
		return domain.Session{}, translate(err)
	}

	eventType := events.TypeSessionArchived
	if status == domain.SessionActive {
		eventType = events.TypeSessionRestored
	}
	s.emit(ctx, identity.UserID, eventType,
		domain.Scope{ProjectID: session.ProjectID, SessionID: session.ID}, nil)
	return session, nil
}

// DeleteSession removes a Session and everything that only existed inside it.
//
// A Session with work still running is refused rather than torn out from under
// a backend that is in the middle of it: the Job would keep going on a machine
// somewhere, reporting to a Session that no longer exists. Stopping it first is
// an act someone has to take deliberately, and the message says so.
//
// The files made in it are the person's call, asked when they delete: kept,
// they stay with the Project, no longer attached to anything; or they go with
// the Session. Keeping is the default, because a file someone downloaded a
// link to should not vanish as a side effect.
//
// The event goes out before the rows are gone, because afterwards there is no
// Session left to attribute it to.
func (s *Service) DeleteSession(ctx context.Context, identity auth.Identity, sessionID domain.SessionID, withArtifacts bool) error {
	session, err := s.store.GetSession(ctx, identity.UserID, sessionID)
	if err != nil {
		return translate(err)
	}

	jobs, err := s.store.ListJobs(ctx, sessionID)
	if err != nil {
		return translate(err)
	}
	for _, job := range jobs {
		if !job.Status.Terminal() {
			return fmt.Errorf("%w: this session has a job that is still %s; stop it first",
				ErrConflict, strings.ToLower(job.Status.String()))
		}
	}

	if withArtifacts {
		ids, err := s.store.SessionArtifactIDs(ctx, identity.UserID, sessionID)
		if err != nil {
			return translate(err)
		}
		for _, id := range ids {
			if err := s.DeleteArtifact(ctx, identity, id); err != nil && !errors.Is(err, ErrNotFound) {
				return err
			}
		}
	}

	s.emit(ctx, identity.UserID, events.TypeSessionDeleted,
		domain.Scope{ProjectID: session.ProjectID, SessionID: session.ID}, nil)

	return translate(s.store.DeleteSession(ctx, identity.UserID, sessionID))
}

// SetSessionWorkingDirectory changes the initial cwd of a Session. The change is
// explicit: a temporary cd by the agent never mutates it.
func (s *Service) SetSessionWorkingDirectory(ctx context.Context, identity auth.Identity, sessionID domain.SessionID, dirID *domain.KnownDirectoryID) (domain.Session, error) {
	current, err := s.store.GetSession(ctx, identity.UserID, sessionID)
	if err != nil {
		return domain.Session{}, translate(err)
	}
	if dirID != nil {
		dir, err := s.store.GetKnownDirectory(ctx, identity.UserID, *dirID)
		if err != nil {
			return domain.Session{}, translate(err)
		}
		if dir.ProjectID != current.ProjectID {
			return domain.Session{}, fmt.Errorf("%w: the directory belongs to another project", ErrInvalid)
		}
	}

	session, err := s.store.SetSessionWorkingDirectory(ctx, identity.UserID, sessionID, dirID)
	if err != nil {
		return domain.Session{}, translate(err)
	}
	s.emit(ctx, identity.UserID, events.TypeWorkingDirectoryChanged,
		domain.Scope{ProjectID: session.ProjectID, SessionID: session.ID},
		WorkingDirectoryChangedPayload{KnownDirectoryID: session.WorkingDirectoryID})
	return session, nil
}

// ListSessions returns the Sessions of a Project.
func (s *Service) ListSessions(ctx context.Context, identity auth.Identity, projectID domain.ProjectID, includeArchived bool) ([]domain.Session, error) {
	sessions, err := s.store.ListSessions(ctx, identity.UserID, projectID, includeArchived)
	return sessions, translate(err)
}

// Snapshot is what a client receives when it opens a Session: current state, a
// recent window of history, the pending attention items and the cursor to
// stream from. A client never replays the whole event log to rebuild state.
type Snapshot struct {
	Session   domain.Session    `json:"session"`
	Runs      []domain.Run      `json:"runs"`
	Jobs      []domain.Job      `json:"jobs"`
	Events    []events.Envelope `json:"events"`
	Attention domain.Attention  `json:"attention"`
	Cursor    domain.Sequence   `json:"cursor"`
}

// SessionSnapshot assembles the initial load of a Session.
func (s *Service) SessionSnapshot(ctx context.Context, identity auth.Identity, sessionID domain.SessionID, historyLimit int) (Snapshot, error) {
	session, err := s.store.GetSession(ctx, identity.UserID, sessionID)
	if err != nil {
		return Snapshot{}, translate(err)
	}
	if historyLimit <= 0 || historyLimit > 500 {
		historyLimit = 100
	}

	snapshot := Snapshot{Session: session}
	if snapshot.Runs, err = s.store.ListRuns(ctx, sessionID); err != nil {
		return Snapshot{}, translate(err)
	}
	if snapshot.Jobs, err = s.store.ListJobs(ctx, sessionID); err != nil {
		return Snapshot{}, translate(err)
	}
	if snapshot.Events, err = s.store.SessionEvents(ctx, identity.UserID, sessionID, 0, historyLimit); err != nil {
		return Snapshot{}, translate(err)
	}
	if snapshot.Attention, err = s.Attention(ctx, identity, sessionID); err != nil {
		return Snapshot{}, err
	}
	if snapshot.Cursor, err = s.store.LatestSequence(ctx); err != nil {
		return Snapshot{}, translate(err)
	}
	return snapshot, nil
}

// SessionHistory pages backwards through the timeline of a Session.
func (s *Service) SessionHistory(ctx context.Context, identity auth.Identity, sessionID domain.SessionID, before domain.Sequence, limit int) ([]events.Envelope, error) {
	if _, err := s.store.GetSession(ctx, identity.UserID, sessionID); err != nil {
		return nil, translate(err)
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	history, err := s.store.SessionEvents(ctx, identity.UserID, sessionID, before, limit)
	return history, translate(err)
}

// Attention returns what is currently waiting for the user, for one Session or
// across every Session when sessionID is empty. It is current state, never a
// count of unread events.
func (s *Service) Attention(ctx context.Context, identity auth.Identity, sessionID domain.SessionID) (domain.Attention, error) {
	validations, err := s.store.PendingValidations(ctx, identity.UserID, sessionID)
	if err != nil {
		return domain.Attention{}, translate(err)
	}
	inputs, err := s.store.PendingUserInputs(ctx, identity.UserID, sessionID)
	if err != nil {
		return domain.Attention{}, translate(err)
	}

	attention := domain.Attention{Validations: validations, UserInputs: inputs}
	s.markRelevance(ctx, identity.UserID, &attention)
	s.addContext(ctx, identity.UserID, &attention)
	return attention, nil
}

// addContext says where each pending request comes from. Context is a help to
// the person deciding; losing it must not cost them the list itself.
func (s *Service) addContext(ctx context.Context, ownerID domain.UserID, attention *domain.Attention) {
	runs := make([]domain.RunID, 0, len(attention.Validations)+len(attention.UserInputs))
	for _, item := range attention.Validations {
		runs = append(runs, item.Scope.RunID)
	}
	for _, item := range attention.UserInputs {
		runs = append(runs, item.Scope.RunID)
	}
	contexts, err := s.store.AttentionContexts(ctx, ownerID, runs)
	if err != nil {
		s.logger.Error("cannot read the context of pending work", slog.String("error", err.Error()))
		return
	}
	for i := range attention.Validations {
		if c, ok := contexts[attention.Validations[i].Scope.RunID]; ok {
			attention.Validations[i].Context = &c
		}
	}
	for i := range attention.UserInputs {
		if c, ok := contexts[attention.UserInputs[i].Scope.RunID]; ok {
			attention.UserInputs[i].Context = &c
		}
	}
}

// markRelevance fills in the notification relevance of specification section 6.
//
// Core does not push anything itself, and V1 has no device registry. What it can
// say is which client started the work and whether that client is still
// watching, which is exactly what a notifier needs to avoid making an unrelated
// device ring for work someone is following on their screen.
func (s *Service) markRelevance(ctx context.Context, ownerID domain.UserID, attention *domain.Attention) {
	ids := make([]domain.JobID, 0, len(attention.Validations)+len(attention.UserInputs))
	for _, item := range attention.Validations {
		ids = append(ids, item.Scope.JobID)
	}
	for _, item := range attention.UserInputs {
		ids = append(ids, item.Scope.JobID)
	}

	origins, err := s.store.JobOriginChannels(ctx, ids)
	if err != nil {
		// Relevance is a hint. Losing it must not cost the user the list of what
		// is actually waiting for them.
		s.logger.Error("cannot read the origin channels of pending work",
			slog.String("error", err.Error()))
		return
	}

	// Looked up once per channel: a user holds a handful of streams, and the
	// answer is the same for every item that came from the same client.
	watching := make(map[domain.Channel]bool, 4)
	notify := func(origin domain.Channel) bool {
		if origin == "" {
			return true
		}
		live, known := watching[origin]
		if !known {
			live = s.broker.Watching(ownerID, origin)
			watching[origin] = live
		}
		// The client that started the work is receiving the live events, so a
		// notification would only repeat what it already shows.
		return !live
	}

	for i := range attention.Validations {
		origin := origins[attention.Validations[i].Scope.JobID]
		attention.Validations[i].OriginChannel = origin
		attention.Validations[i].Notify = notify(origin)
	}
	for i := range attention.UserInputs {
		origin := origins[attention.UserInputs[i].Scope.JobID]
		attention.UserInputs[i].OriginChannel = origin
		attention.UserInputs[i].Notify = notify(origin)
	}
	// Delegated requests are addressed to the manager. Keep them inspectable
	// and resolvable by the owner, but avoid asking them to converse with each
	// worker separately. Detached children revert to ordinary sessions.
	managed := make(map[domain.SessionID]bool)
	checkManaged := func(id domain.SessionID) bool {
		if value, ok := managed[id]; ok {
			return value
		}
		session, err := s.store.GetSession(ctx, ownerID, id)
		value := false
		if err == nil && session.ManagerSessionID != nil {
			manager, managerErr := s.store.GetSession(ctx, ownerID, *session.ManagerSessionID)
			value = managerErr == nil && manager.Status == domain.SessionActive
		}
		managed[id] = value
		return value
	}
	for i := range attention.UserInputs {
		if checkManaged(attention.UserInputs[i].Scope.SessionID) {
			attention.UserInputs[i].Notify = false
		}
	}
	for i := range attention.Validations {
		if checkManaged(attention.Validations[i].Scope.SessionID) {
			attention.Validations[i].Notify = false
		}
	}
}

// checkBackendUsable rejects a backend that cannot run CODE work.
func (s *Service) checkBackendUsable(ctx context.Context, identity auth.Identity, instanceID domain.BackendInstanceID) error {
	instance, err := s.store.GetBackendInstance(ctx, identity.UserID, instanceID)
	if err != nil {
		return translate(err)
	}
	if instance.OwnershipStatus == domain.BackendRevoked {
		return fmt.Errorf("%w: the backend is revoked", ErrConflict)
	}
	if !instance.HasCapability(domain.CapabilityCode) {
		return fmt.Errorf("%w: the backend does not advertise the CODE capability", ErrConflict)
	}
	return nil
}

// checkWorkingDirectory makes sure the chosen logical directory belongs to the
// project.
//
// A missing binding on the chosen backend is deliberately not an error: the
// backend resolves the directory when the Job starts, searching its discovery
// roots and asking the user if it has to (spec section 11). Refusing here would
// make the user do by hand what the backend is in a position to work out.
func (s *Service) checkWorkingDirectory(ctx context.Context, identity auth.Identity, projectID domain.ProjectID, _ domain.BackendInstanceID, dirID *domain.KnownDirectoryID) error {
	if dirID == nil {
		return nil
	}
	dir, err := s.store.GetKnownDirectory(ctx, identity.UserID, *dirID)
	if err != nil {
		return translate(err)
	}
	if dir.ProjectID != projectID {
		return fmt.Errorf("%w: the directory belongs to another project", ErrInvalid)
	}
	return nil
}

// deriveTitle generates a Session title from its first message. The title is
// cosmetic: it carries no technical meaning and the user can rename it.
func deriveTitle(message string) string {
	line := message
	if idx := strings.IndexAny(line, "\r\n"); idx >= 0 {
		line = line[:idx]
	}
	line = strings.Join(strings.FieldsFunc(line, unicode.IsSpace), " ")
	if line == "" {
		return "Untitled session"
	}

	runes := []rune(line)
	if len(runes) <= titleLimit {
		return line
	}
	return strings.TrimSpace(string(runes[:titleLimit])) + "…"
}

// nativeSessionLimit bounds an adopted provider session id. Core does not know
// the shape each provider uses, only that an id is a short opaque token.
const nativeSessionLimit = 200

// adoptedSession checks a provider session id a client asks to adopt. Core
// cannot tell whether it exists, only the backend can, so this keeps out what
// no provider would mint: whitespace, control characters, and a leading dash a
// command line would read as a flag.
func adoptedSession(id string) (*string, error) {
	if id == "" {
		return nil, nil
	}
	if len(id) > nativeSessionLimit || strings.HasPrefix(id, "-") ||
		strings.IndexFunc(id, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return nil, fmt.Errorf("%w: %q is not a provider session id", ErrInvalid, id)
	}
	return &id, nil
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func workingDirectoryString(id *domain.KnownDirectoryID) *string {
	if id == nil {
		return nil
	}
	value := string(*id)
	return &value
}

// EventsAfter returns the events a client missed since a global sequence,
// across every Session it can see. This is the catch-up behind the SSE cursor.
func (s *Service) EventsAfter(ctx context.Context, identity auth.Identity, after domain.Sequence, limit int) ([]events.Envelope, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	missed, err := s.store.EventsAfter(ctx, identity.UserID, after, limit)
	return missed, translate(err)
}
