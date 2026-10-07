package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/events"
	"github.com/rclsilver/threavia/internal/core/storage/postgres"
	"github.com/rclsilver/threavia/internal/core/tools"
)

// How much of the project knowledge a Run context carries. Section 12 asks for a
// compact structured context, so these are deliberately small: everything else
// is searchable on demand through the Core Tools.
const (
	contextDecisionLimit = 20
	contextTaskLimit     = 20
)

// DispatchJob sends a queued Job to its backend, if that backend is connected
// and its Run has no active Job.
//
// Dispatching is deliberately best-effort and never fails an enqueue: a Job
// waits in the queue until its backend comes back, and Core re-dispatches on
// reconnection. Errors are logged, not returned, because nothing the caller
// could do about them would be correct.
func (s *Service) DispatchJob(ctx context.Context, jobID domain.JobID) {
	jc, err := s.store.LoadJobContext(ctx, jobID)
	if err != nil {
		s.logger.Error("cannot load job context", slog.String("jobId", string(jobID)), slog.String("error", err.Error()))
		return
	}

	from := jc.Job.Status
	if from != domain.JobQueued && from != domain.JobWaitingBackend {
		return
	}

	conn, ok := s.backends.Lookup(jc.BackendInstanceID)
	if !ok {
		s.logger.Debug("job waits for its backend",
			slog.String("jobId", string(jobID)),
			slog.String("backendInstanceId", string(jc.BackendInstanceID)))
		return
	}

	prompt, err := s.store.JobPrompt(ctx, jobID)
	if err != nil {
		s.logger.Error("cannot read the job prompt", slog.String("jobId", string(jobID)), slog.String("error", err.Error()))
		return
	}

	// Reserving the active slot before sending is what keeps a second Job of the
	// same Run from being dispatched concurrently.
	if _, err := s.store.TransitionJob(ctx, jobID, from, domain.JobRunning, nil); err != nil {
		if errors.Is(err, postgres.ErrJobSlotTaken) || errors.Is(err, postgres.ErrNotFound) {
			// Another Job holds the slot, or this one already moved on.
			return
		}
		s.logger.Error("cannot start the job", slog.String("jobId", string(jobID)), slog.String("error", err.Error()))
		return
	}

	command := &backendv1.CoreToBackend{
		CommandId: domain.NewUUID(),
		Message: &backendv1.CoreToBackend_StartJob{
			StartJob: &backendv1.StartJob{
				ProjectId:       string(jc.ProjectID),
				SessionId:       string(jc.SessionID),
				RunId:           string(jc.Run.ID),
				JobId:           string(jobID),
				NativeSessionId: derefString(jc.NativeSessionID),
				Prompt:          prompt,
				ProjectContext:  s.projectContext(ctx, jc),
				ExecutionPolicy: &backendv1.ExecutionPolicy{
					// The breadth of specification section 17 is deferred; the
					// first slice runs interactively, which is what makes the
					// validation flow observable.
					Mode: backendv1.ExecutionMode_EXECUTION_MODE_INTERACTIVE,
				},
			},
		},
	}

	s.dispatched.Store(command.GetCommandId(), dispatchedCommand{jobID: jobID, at: s.now()})
	if !conn.Send(command) {
		s.dispatched.Delete(command.GetCommandId())
		// The connection went away between the lookup and the send. Park the Job
		// so the reconnection picks it up rather than losing it.
		if _, err := s.store.TransitionJob(ctx, jobID, domain.JobRunning, domain.JobWaitingBackend, nil); err != nil {
			s.logger.Error("cannot park the job", slog.String("jobId", string(jobID)), slog.String("error", err.Error()))
		}
		return
	}

	s.logger.Info("job dispatched",
		slog.String("jobId", string(jobID)),
		slog.String("runId", string(jc.Run.ID)),
		slog.String("backendInstanceId", string(jc.BackendInstanceID)),
		slog.Bool("resume", jc.NativeSessionID != nil))
}

// projectContext builds the compact structured context of specification
// section 12. It never carries the project history: everything else is
// searchable on demand.
func (s *Service) projectContext(ctx context.Context, jc postgres.JobContext) *backendv1.ProjectContext {
	pc := &backendv1.ProjectContext{
		ProjectId:          string(jc.ProjectID),
		ProjectName:        jc.ProjectName,
		ProjectDescription: jc.ProjectDesc,
		Tools:              coreToolSpecs(),
	}
	if jc.WorkingDirectoryPath != nil {
		pc.WorkingDirectoryPath = *jc.WorkingDirectoryPath
	}
	if jc.KnownDirectoryID != nil {
		pc.KnownDirectoryId = string(*jc.KnownDirectoryID)
	}

	// Active IMPORTANT decisions only. A superseded or NORMAL one is searchable
	// on demand, and injecting either would spend context on something that is
	// no longer true or not important enough to have been marked so.
	decisions, err := s.store.ImportantDecisions(ctx, jc.ProjectID, contextDecisionLimit)
	if err != nil {
		s.logger.Error("cannot read the project decisions",
			slog.String("projectId", string(jc.ProjectID)), slog.String("error", err.Error()))
	}
	for _, decision := range decisions {
		pc.Decisions = append(pc.Decisions, &backendv1.ContextDecision{
			Id: string(decision.ID), Title: decision.Title, Content: decision.Content,
		})
	}

	// A compact summary of what is open and actionable, not every Task ever
	// filed: in progress first, then what is ready to start.
	tasks, err := s.store.OpenTasks(ctx, jc.ProjectID, contextTaskLimit)
	if err != nil {
		s.logger.Error("cannot read the project tasks",
			slog.String("projectId", string(jc.ProjectID)), slog.String("error", err.Error()))
	}
	for _, task := range tasks {
		pc.Tasks = append(pc.Tasks, &backendv1.ContextTask{
			Id: string(task.ID), Title: task.Title, Status: task.Status.String(),
		})
	}

	return pc
}

// coreToolSpecs renders the Core Tools for the wire. A backend never hardcodes
// their names: it learns them here, at Job start.
func coreToolSpecs() []*backendv1.CoreToolSpec {
	specs := tools.Specs()
	out := make([]*backendv1.CoreToolSpec, 0, len(specs))
	for _, spec := range specs {
		encoded := &backendv1.CoreToolSpec{
			Name:        spec.Name.String(),
			Description: spec.Description,
		}
		if len(spec.InputSchema) > 0 {
			var schema map[string]any
			if err := json.Unmarshal(spec.InputSchema, &schema); err == nil {
				encoded.InputSchema, _ = structpb.NewStruct(schema)
			}
		}
		out = append(out, encoded)
	}
	return out
}

// dispatchNext starts the oldest queued Job of a Run, once its active slot is
// free. Queued Jobs are FIFO.
func (s *Service) dispatchNext(ctx context.Context, runID domain.RunID) {
	next, err := s.store.NextQueuedJob(ctx, runID)
	if errors.Is(err, postgres.ErrNotFound) {
		return
	}
	if err != nil {
		s.logger.Error("cannot read the job queue", slog.String("runId", string(runID)), slog.String("error", err.Error()))
		return
	}
	s.DispatchJob(ctx, next.ID)
}

// CancelJob asks for a Job to stop.
//
// A queued Job has nothing running on a backend and is cancelled directly. A
// live one goes to CANCELLING and only reaches CANCELLED once the backend
// confirms the stop; if the backend is offline it stays CANCELLING and is
// reconciled on reconnection.
func (s *Service) CancelJob(ctx context.Context, identity auth.Identity, jobID domain.JobID, reason string) (domain.Job, error) {
	job, err := s.store.GetJob(ctx, identity.UserID, jobID)
	if err != nil {
		return domain.Job{}, translate(err)
	}
	if job.Status.Terminal() {
		return job, nil // Cancelling a finished Job is a no-op, so retries are safe.
	}
	if job.Status == domain.JobCancelling {
		return job, nil
	}

	jc, err := s.store.LoadJobContext(ctx, jobID)
	if err != nil {
		return domain.Job{}, translate(err)
	}
	scope := domain.Scope{ProjectID: jc.ProjectID, SessionID: jc.SessionID, RunID: jc.Run.ID, JobID: jobID}

	if job.Status == domain.JobQueued {
		updated, err := s.store.TransitionJob(ctx, jobID, domain.JobQueued, domain.JobCancelled, nil)
		if err != nil {
			return domain.Job{}, translate(err)
		}
		s.emit(ctx, identity.UserID, events.TypeJobCancelled, scope, JobEndedPayload{Reason: reason})
		return updated, nil
	}

	updated, err := s.store.TransitionJob(ctx, jobID, job.Status, domain.JobCancelling, nil)
	if err != nil {
		return domain.Job{}, translate(err)
	}

	if conn, ok := s.backends.Lookup(jc.BackendInstanceID); ok {
		conn.Send(&backendv1.CoreToBackend{
			CommandId: domain.NewUUID(),
			Message: &backendv1.CoreToBackend_CancelJob{
				CancelJob: &backendv1.CancelJob{
					RunId: string(jc.Run.ID), JobId: string(jobID), Reason: reason,
				},
			},
		})
	} else {
		s.logger.Info("cancellation queued until the backend reconnects",
			slog.String("jobId", string(jobID)),
			slog.String("backendInstanceId", string(jc.BackendInstanceID)))
	}
	return updated, nil
}

// DeleteQueuedJob removes a Job that has not started yet.
func (s *Service) DeleteQueuedJob(ctx context.Context, identity auth.Identity, jobID domain.JobID) error {
	return translate(s.store.DeleteQueuedJob(ctx, identity.UserID, jobID))
}

// ResolveValidation records the user decision on a pending permission request.
//
// The transition is atomic: the first valid response wins, and a second client
// answering the same request is told it is already resolved.
func (s *Service) ResolveValidation(ctx context.Context, identity auth.Identity, id domain.ValidationRequestID, approved bool, channel, note string) (domain.ValidationRequest, error) {
	if _, err := s.store.GetValidationRequest(ctx, identity.UserID, id); err != nil {
		return domain.ValidationRequest{}, translate(err)
	}

	resolved, err := s.store.ResolveValidationRequest(ctx, id, approved, identity.UserID, channel, note)
	if err != nil {
		if errors.Is(err, postgres.ErrNotFound) {
			return domain.ValidationRequest{}, fmt.Errorf("%w: the request is already resolved", ErrConflict)
		}
		return domain.ValidationRequest{}, translate(err)
	}

	// The receipt: who decided what, through which client, over which canonical
	// payload hash.
	s.emit(ctx, identity.UserID, events.TypeValidationResolved, resolved.Scope, ValidationResolvedPayload{
		ValidationID:  string(resolved.ID),
		Approved:      approved,
		ActorUserID:   string(identity.UserID),
		Channel:       channel,
		PayloadSHA256: resolved.PayloadSHA256,
		Note:          note,
	})

	s.resumeAfterAttention(ctx, resolved.Scope.JobID, domain.JobWaitingValidation)
	s.sendToBackend(ctx, resolved.Scope, &backendv1.CoreToBackend{
		Message: &backendv1.CoreToBackend_ValidationResolution{
			ValidationResolution: &backendv1.ValidationResolution{
				RunId:            string(resolved.Scope.RunID),
				JobId:            string(resolved.Scope.JobID),
				RequestId:        resolved.BackendRequestID,
				Approved:         approved,
				ResolvedByUserId: string(identity.UserID),
				ResolvedAt:       timestamppb.New(s.now()),
				Note:             note,
			},
		},
	})
	return resolved, nil
}

// ResolveUserInput records the user answer to a pending input request.
func (s *Service) ResolveUserInput(ctx context.Context, identity auth.Identity, id domain.UserInputRequestID, value, channel string) (domain.UserInputRequest, error) {
	if _, err := s.store.GetUserInputRequest(ctx, identity.UserID, id); err != nil {
		return domain.UserInputRequest{}, translate(err)
	}

	resolved, err := s.store.ResolveUserInputRequest(ctx, id, value, identity.UserID, channel)
	if err != nil {
		if errors.Is(err, postgres.ErrNotFound) {
			return domain.UserInputRequest{}, fmt.Errorf("%w: the request is already resolved", ErrConflict)
		}
		return domain.UserInputRequest{}, translate(err)
	}

	s.emit(ctx, identity.UserID, events.TypeUserInputResolved, resolved.Scope, UserInputResolvedPayload{
		RequestID:   string(resolved.ID),
		Value:       value,
		ActorUserID: string(identity.UserID),
		Channel:     channel,
	})

	s.resumeAfterAttention(ctx, resolved.Scope.JobID, domain.JobWaitingInput)
	s.sendToBackend(ctx, resolved.Scope, &backendv1.CoreToBackend{
		Message: &backendv1.CoreToBackend_UserInputResolution{
			UserInputResolution: &backendv1.UserInputResolution{
				RunId:            string(resolved.Scope.RunID),
				JobId:            string(resolved.Scope.JobID),
				RequestId:        resolved.BackendRequestID,
				Value:            value,
				ResolvedByUserId: string(identity.UserID),
				ResolvedAt:       timestamppb.New(s.now()),
			},
		},
	})
	return resolved, nil
}

// resumeAfterAttention moves a Job back to RUNNING once the item it was waiting
// on is answered. A Job that already moved on is left alone.
func (s *Service) resumeAfterAttention(ctx context.Context, jobID domain.JobID, waiting domain.JobStatus) {
	if _, err := s.store.TransitionJob(ctx, jobID, waiting, domain.JobRunning, nil); err != nil {
		if !errors.Is(err, postgres.ErrNotFound) {
			s.logger.Error("cannot resume the job",
				slog.String("jobId", string(jobID)), slog.String("error", err.Error()))
		}
	}
}

// sendToBackend delivers a command to the backend executing a scope, if it is
// connected.
func (s *Service) sendToBackend(ctx context.Context, scope domain.Scope, message *backendv1.CoreToBackend) {
	run, err := s.store.RunByID(ctx, scope.RunID)
	if err != nil {
		s.logger.Error("cannot resolve the run backend",
			slog.String("runId", string(scope.RunID)), slog.String("error", err.Error()))
		return
	}
	conn, ok := s.backends.Lookup(run.BackendInstanceID)
	if !ok {
		s.logger.Warn("the backend is offline, the resolution will be reconciled on reconnection",
			slog.String("runId", string(scope.RunID)),
			slog.String("backendInstanceId", string(run.BackendInstanceID)))
		return
	}
	message.CommandId = domain.NewUUID()
	conn.Send(message)
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
