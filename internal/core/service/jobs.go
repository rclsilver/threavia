package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

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
	// Detached from whatever asked for it. A dispatch is called from an HTTP
	// handler, from a gRPC stream and from reconciliation, and every one of
	// those contexts can end while this is still reading what the Job needs —
	// the request answered, the stream closed. Cancellation there used to land
	// as a handful of logged errors and a Job sent anyway, which is the worst
	// of both: the work starts, without the policy or the project memory it was
	// supposed to carry.
	ctx = context.WithoutCancel(ctx)

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

	// The policy travels with the Job: the backend gate enforces it, so a limit
	// is a limit rather than a request the model may decline to honour.
	policy, err := s.store.EffectivePolicy(ctx, jobID)
	if err != nil {
		s.logger.Error("cannot read the execution policy", slog.String("jobId", string(jobID)), slog.String("error", err.Error()))
		return
	}

	// Built before the slot is reserved: a Job sent without the knowledge it was
	// supposed to carry is worse than a Job that waits. An agent cannot tell
	// that its project memory is missing — it just works as if the project had
	// none, and the decisions someone recorded so every later session would
	// know go unread.
	projectContext, err := s.projectContext(ctx, jc)
	if err != nil {
		s.logger.Error("cannot build the project context, leaving the job queued",
			slog.String("jobId", string(jobID)), slog.String("error", err.Error()))
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
				ProjectContext:  projectContext,
				ExecutionPolicy: policyToProto(policy),
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
//
// A read that fails is returned rather than logged and skipped. An agent has no
// way to tell an empty project from one whose memory could not be read: it
// simply works as though nothing had ever been decided, and the ruling someone
// recorded so that every later session would know goes unread. Refusing to
// build the context leaves the Job queued, which is recoverable; sending it
// without is not.
func (s *Service) projectContext(ctx context.Context, jc postgres.JobContext) (*backendv1.ProjectContext, error) {
	pc := &backendv1.ProjectContext{
		ProjectId:           string(jc.ProjectID),
		ProjectName:         jc.ProjectName,
		ProjectDescription:  jc.ProjectDesc,
		ProjectInstructions: jc.ProjectInstructions,
		Tools:               coreToolSpecs(),
	}
	if jc.WorkingDirectoryPath != nil {
		pc.WorkingDirectoryPath = *jc.WorkingDirectoryPath
	}
	if jc.KnownDirectoryID != nil {
		pc.KnownDirectoryId = string(*jc.KnownDirectoryID)
		pc.KnownDirectoryName = jc.KnownDirectoryName
		pc.KnownDirectoryGitRemote = jc.KnownDirectoryGitRemote
	}

	// Active IMPORTANT decisions only. A superseded or NORMAL one is searchable
	// on demand, and injecting either would spend context on something that is
	// no longer true or not important enough to have been marked so.
	decisions, err := s.store.ImportantDecisions(ctx, jc.ProjectID, contextDecisionLimit)
	if err != nil {
		return nil, fmt.Errorf("read the project decisions: %w", err)
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
		return nil, fmt.Errorf("read the project tasks: %w", err)
	}
	for _, task := range tasks {
		pc.Tasks = append(pc.Tasks, &backendv1.ContextTask{
			Id: string(task.ID), Title: task.Title, Status: task.Status.String(),
		})
	}

	// Core-managed Skills: identity only. The backend fetches and caches the
	// bundles it does not already have, so a Job never carries their content.
	installed, err := s.store.ListSkills(ctx, jc.ProjectID)
	if err != nil {
		return nil, fmt.Errorf("read the project skills: %w", err)
	}
	for _, skill := range installed {
		pc.Skills = append(pc.Skills, &backendv1.ProjectSkill{
			SkillId:           string(skill.ID),
			Name:              skill.Name,
			Description:       skill.Description,
			InstalledRevision: skill.InstalledRevision,
			BundleSha256:      skill.BundleSHA256,
		})
	}

	return pc, nil
}

// coreToolSpecs renders the Core Tools for the wire. A backend never hardcodes
// their names: it learns them here, at Job start.
func coreToolSpecs() []*backendv1.CoreToolSpec {
	specs := tools.Specs()
	out := make([]*backendv1.CoreToolSpec, 0, len(specs))
	for _, spec := range specs {
		encoded := &backendv1.CoreToolSpec{
			Name:               spec.Name.String(),
			Description:        spec.Description,
			FileInput:          spec.FileInput,
			RequiresValidation: spec.RequiresValidation,
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
		Title:         resolved.Title,
		Note:          note,
	})

	s.audit(ctx, identity, postgres.AuditEntry{
		Action:        "validation.resolved",
		ProjectID:     string(resolved.Scope.ProjectID),
		SessionID:     string(resolved.Scope.SessionID),
		JobID:         string(resolved.Scope.JobID),
		SubjectID:     string(resolved.ID),
		Channel:       channel,
		PayloadSHA256: resolved.PayloadSHA256,
		Detail:        mustJSON(map[string]any{"approved": approved, "title": resolved.Title, "note": note}),
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

func derefUserID(value *domain.UserID) string {
	if value == nil {
		return ""
	}
	return string(*value)
}

// resolvedAt renders the moment a request was answered. A resolved request
// always has one; a nil is a row that contradicts its own check constraint,
// and an absent timestamp says that more honestly than a zero one.
func resolvedAt(value *time.Time) *timestamppb.Timestamp {
	if value == nil {
		return nil
	}
	return timestamppb.New(*value)
}

// policyToProto renders an execution policy for the wire.
func policyToProto(policy domain.ExecutionPolicy) *backendv1.ExecutionPolicy {
	mode := backendv1.ExecutionMode_EXECUTION_MODE_INTERACTIVE
	switch policy.Mode {
	case domain.ExecutionGuarded:
		mode = backendv1.ExecutionMode_EXECUTION_MODE_GUARDED
	case domain.ExecutionSupervised:
		mode = backendv1.ExecutionMode_EXECUTION_MODE_SUPERVISED
	case domain.ExecutionAutonomous:
		mode = backendv1.ExecutionMode_EXECUTION_MODE_AUTONOMOUS
	}
	return &backendv1.ExecutionPolicy{
		Mode:                 mode,
		AllowFilesystemWrite: policy.AllowFilesystemWrite,
		AllowGitCommit:       policy.AllowGitCommit,
		AllowGitPush:         policy.AllowGitPush,
		AllowNetwork:         policy.AllowNetwork,
		MaxDurationSeconds:   uint32(policy.MaxDurationSeconds),
		MaxActions:           uint32(policy.MaxActions),
		Rules:                rulesToProto(policy.Rules),
		Supervision:          policy.Supervision,
	}
}

var effectToProto = map[domain.PermissionEffect]backendv1.PermissionEffect{
	domain.PermissionAllow: backendv1.PermissionEffect_PERMISSION_EFFECT_ALLOW,
	domain.PermissionAsk:   backendv1.PermissionEffect_PERMISSION_EFFECT_ASK,
	domain.PermissionDeny:  backendv1.PermissionEffect_PERMISSION_EFFECT_DENY,
}

var capabilityToProto = map[domain.PermissionCapability]backendv1.PermissionCapability{
	domain.CapabilityShell:     backendv1.PermissionCapability_PERMISSION_CAPABILITY_SHELL,
	domain.CapabilityFileRead:  backendv1.PermissionCapability_PERMISSION_CAPABILITY_FILE_READ,
	domain.CapabilityFileWrite: backendv1.PermissionCapability_PERMISSION_CAPABILITY_FILE_WRITE,
	domain.CapabilityNetwork:   backendv1.PermissionCapability_PERMISSION_CAPABILITY_NETWORK,
	domain.CapabilityGitCommit: backendv1.PermissionCapability_PERMISSION_CAPABILITY_GIT_COMMIT,
	domain.CapabilityGitPush:   backendv1.PermissionCapability_PERMISSION_CAPABILITY_GIT_PUSH,
	domain.CapabilityTool:      backendv1.PermissionCapability_PERMISSION_CAPABILITY_TOOL,
}

// rulesToProto renders the permission rules for the wire.
//
// A rule whose effect or capability this Core does not know is dropped rather
// than sent as UNSPECIFIED: a backend reading an unspecified capability has no
// honest way to apply it, and a rule it cannot apply must not look applied.
func rulesToProto(rules []domain.PermissionRule) []*backendv1.PermissionRule {
	if len(rules) == 0 {
		return nil
	}
	out := make([]*backendv1.PermissionRule, 0, len(rules))
	for _, rule := range rules {
		effect, known := effectToProto[rule.Effect]
		capability, classified := capabilityToProto[rule.Capability]
		if !known || !classified {
			continue
		}
		out = append(out, &backendv1.PermissionRule{
			Effect:     effect,
			Capability: capability,
			Match:      rule.Match,
			Note:       rule.Note,
		})
	}
	return out
}
