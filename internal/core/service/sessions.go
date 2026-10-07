package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"unicode"

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
		},
	}
	result.Run = domain.Run{
		ID:                domain.NewRunID(),
		SessionID:         result.Session.ID,
		BackendInstanceID: in.BackendInstanceID,
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
		if err := tx.CreateRun(ctx, &result.Run); err != nil {
			return err
		}
		if err := tx.CreateJob(ctx, &result.Job); err != nil {
			return err
		}
		return s.appendAll(ctx, tx, b,
			record{events.TypeSessionCreated, domain.Scope{ProjectID: scope.ProjectID, SessionID: scope.SessionID},
				SessionCreatedPayload{Title: result.Session.Title, WorkingDirectoryID: result.Session.WorkingDirectoryID}},
			record{events.TypeRunCreated, domain.Scope{ProjectID: scope.ProjectID, SessionID: scope.SessionID, RunID: scope.RunID},
				RunCreatedPayload{BackendInstanceID: string(in.BackendInstanceID)}},
			record{events.TypeJobCreated, scope, JobCreatedPayload{RunID: string(scope.RunID)}},
			record{events.TypeUserMessage, scope, UserMessagePayload{Text: message}},
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
	job, err := s.store.JobByIdempotencyKey(ctx, key)
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
func (s *Service) PostMessage(ctx context.Context, identity auth.Identity, sessionID domain.SessionID, message, idempotencyKey string) (domain.Job, error) {
	message = strings.TrimSpace(message)
	if message == "" {
		return domain.Job{}, fmt.Errorf("%w: the message cannot be empty", ErrInvalid)
	}

	if idempotencyKey != "" {
		job, err := s.store.JobByIdempotencyKey(ctx, idempotencyKey)
		if err == nil {
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
			record{events.TypeUserMessage, scope, UserMessagePayload{Text: message}},
		)
	})
	if err != nil {
		return domain.Job{}, translate(err)
	}

	s.flush(b)
	s.DispatchJob(ctx, job.ID)
	return job, nil
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
// The event goes out before the rows are gone, because afterwards there is no
// Session left to attribute it to.
func (s *Service) DeleteSession(ctx context.Context, identity auth.Identity, sessionID domain.SessionID) error {
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
	return attention, nil
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
