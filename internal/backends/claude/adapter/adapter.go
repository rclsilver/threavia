package adapter

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"google.golang.org/protobuf/types/known/structpb"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/backends/claude/mcp"
	"github.com/rclsilver/threavia/internal/backends/claude/policy"
	"github.com/rclsilver/threavia/internal/backends/claude/runner"
	"github.com/rclsilver/threavia/internal/backends/claude/skills"
	"github.com/rclsilver/threavia/pkg/backend-sdk/client"
	"github.com/rclsilver/threavia/pkg/backend-sdk/state"
)

// ErrJobAbandoned is delivered to anything still waiting on a Job that ended.
var ErrJobAbandoned = errors.New("the job ended before the request was answered")

// Adapter implements the backend SDK Handler for Claude Code, and is the only
// place where Claude concepts and Threavia concepts meet.
type Adapter struct {
	client.BaseHandler

	cfg    Config
	runner runner.Runner
	store  state.Store

	// skillCache holds the Core-managed bundles this backend has unpacked;
	// localSkills are the ones that exist only here (spec section 18).
	skillCache  *skills.Cache
	localSkills []skills.Local
	logger      *slog.Logger

	mu      sync.Mutex
	client  *client.Client
	jobs    map[string]*jobState
	waiters map[string]*waiter
}

// jobState is what the adapter remembers about a Job it is running.
type jobState struct {
	runID    string
	requests map[string]struct{}
	// knownDirectoryID is the KnownDirectory Core resolved for this Job, when
	// there is one. It attributes a change summary to a directory.
	knownDirectoryID string
	// policy is what Core allows this Job to do. The permission gate consults it
	// before anything reaches the user, so a forbidden action is refused rather
	// than offered as a choice.
	policy policy.Policy
}

// waiter is a pending question, blocked until Core brings an answer back.
type waiter struct {
	jobID    string
	approved chan mcp.Decision
	answer   chan string
	failed   chan error
}

// New builds the Claude adapter.
func New(cfg Config, claudeRunner runner.Runner, store state.Store, logger *slog.Logger) *Adapter {
	return &Adapter{
		cfg:         cfg,
		runner:      claudeRunner,
		store:       store,
		logger:      logger,
		skillCache:  skills.NewCache(cfg.Claude.SkillCachePath),
		localSkills: skills.Discover(cfg.Claude.LocalSkillRoots),
		jobs:        make(map[string]*jobState),
		waiters:     make(map[string]*waiter),
	}
}

// Bind gives the adapter the SDK client it reports through. It is called once,
// after the client is built, because the client needs the handler at
// construction time.
func (a *Adapter) Bind(c *client.Client) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.client = c
}

func (a *Adapter) sdk() *client.Client {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.client
}

// OnConnected reports the initial operational status to Core.
//
// A missing Claude Code CLI is reported as DEGRADED with an explanatory
// condition rather than as a startup failure: the backend stays reachable and
// the user can see why it cannot work.
func (a *Adapter) OnConnected(ctx context.Context, welcome *backendv1.Welcome) error {
	sdk := a.sdk()
	if sdk == nil {
		return nil
	}

	status := backendv1.BackendOperationalStatus_BACKEND_OPERATIONAL_STATUS_READY
	providerAuth := backendv1.ProviderAuthState_PROVIDER_AUTH_STATE_AUTHENTICATED
	var conditions []*backendv1.Condition

	if err := a.runner.Available(); err != nil {
		status = backendv1.BackendOperationalStatus_BACKEND_OPERATIONAL_STATUS_DEGRADED
		providerAuth = backendv1.ProviderAuthState_PROVIDER_AUTH_STATE_AUTHENTICATION_REQUIRED
		conditions = append(conditions, &backendv1.Condition{
			Type:    "ProviderAvailable",
			Status:  "False",
			Reason:  "ExecutableNotFound",
			Message: err.Error(),
		})
	}

	// Reported on every connection: Core keeps the metadata of what only exists
	// here, so a handoff can say another backend cannot run a given Skill.
	a.reportSkills(ctx)

	a.logger.Info("reporting backend status",
		slog.String("connectionId", welcome.GetConnectionId()),
		slog.String("status", status.String()))
	return sdk.SendStatus(ctx, status, providerAuth, conditions...)
}

// OnDisconnected logs the loss of the control stream. Local execution keeps
// running: a Core outage must never stop agent work, and events are buffered
// durably until Core comes back.
func (a *Adapter) OnDisconnected(_ context.Context, cause error) {
	if cause != nil {
		a.logger.Warn("disconnected from core", slog.String("cause", cause.Error()))
		return
	}
	a.logger.Info("disconnected from core")
}

// OnStartJob starts or resumes the provider session for a Run.
//
// It returns as soon as the work is accepted: a Job runs for minutes and the
// control stream must stay responsive, so the process is driven from its own
// goroutine and everything it produces travels back as events.
func (a *Adapter) OnStartJob(ctx context.Context, cmd *backendv1.StartJob) error {
	workingDirectory := cmd.GetProjectContext().GetWorkingDirectoryPath()
	if workingDirectory == "" {
		// The Session has no working directory, so this backend decides where
		// unscoped work happens rather than inheriting its own launch directory.
		workingDirectory = a.cfg.Claude.DefaultWorkingDirectory
	}

	params := runner.StartParams{
		RunID:               cmd.GetRunId(),
		JobID:               cmd.GetJobId(),
		NativeSessionID:     cmd.GetNativeSessionId(),
		WorkingDirectory:    workingDirectory,
		Prompt:              cmd.GetPrompt(),
		ProjectName:         cmd.GetProjectContext().GetProjectName(),
		ProjectDescription:  cmd.GetProjectContext().GetProjectDescription(),
		CoreTools:           coreTools(cmd.GetProjectContext()),
		Policy:              policy.From(cmd.GetExecutionPolicy()),
		ProjectInstructions: cmd.GetProjectContext().GetProjectInstructions(),
		LocalInstructions:   a.cfg.Claude.LocalInstructions,
	}
	if params.RunID == "" || params.JobID == "" {
		return fmt.Errorf("a run id and a job id are required")
	}

	a.mu.Lock()
	if _, busy := a.jobs[params.JobID]; busy {
		a.mu.Unlock()
		return nil // Already running: a redelivered command is not a new Job.
	}
	a.jobs[params.JobID] = &jobState{
		runID:            params.RunID,
		requests:         make(map[string]struct{}),
		policy:           policy.From(cmd.GetExecutionPolicy()),
		knownDirectoryID: cmd.GetProjectContext().GetKnownDirectoryId(),
	}
	a.mu.Unlock()

	if err := a.store.SaveJob(context.WithoutCancel(ctx), state.JobRecord{
		JobID:  params.JobID,
		RunID:  params.RunID,
		Status: backendv1.JobStatus_JOB_STATUS_RUNNING,
	}); err != nil {
		a.logger.Error("cannot record the job locally", slog.String("error", err.Error()))
	}
	a.updateActiveRuns()

	// Skills are prepared before the process starts: a bundle this backend does
	// not have is fetched over the control stream and unpacked once.
	params.SkillDirectory = a.prepareSkills(ctx, params.JobID, cmd.GetProjectContext().GetSkills())

	// Detached from the command context: the Job outlives the message that
	// started it.
	go a.execute(context.WithoutCancel(ctx), params)
	return nil
}

// execute drives one Job and cleans up after it.
func (a *Adapter) execute(ctx context.Context, params runner.StartParams) {
	defer func() {
		a.releaseJob(params.JobID, ErrJobAbandoned)
		a.releaseSkills(params.JobID)
		a.updateActiveRuns()
	}()

	if err := a.runner.Run(ctx, params, a); err != nil {
		a.logger.Error("job failed to run",
			slog.String("jobId", params.JobID), slog.String("error", err.Error()))
	}
}

// OnCancelJob asks the runner to stop. Core only moves the Job to CANCELLED
// once the backend confirms the stop with a job cancelled event.
func (a *Adapter) OnCancelJob(_ context.Context, cmd *backendv1.CancelJob) error {
	a.logger.Info("cancel requested",
		slog.String("jobId", cmd.GetJobId()), slog.String("reason", cmd.GetReason()))

	if err := a.runner.Cancel(cmd.GetJobId()); err != nil {
		if errors.Is(err, runner.ErrUnknownJob) {
			// Nothing is running here. Reconciliation converges Core, so this is
			// not a command failure.
			a.logger.Info("nothing to cancel for this job", slog.String("jobId", cmd.GetJobId()))
			return nil
		}
		return err
	}
	return nil
}

// OnValidationResolution delivers a permission decision to the blocked tool
// call.
func (a *Adapter) OnValidationResolution(_ context.Context, cmd *backendv1.ValidationResolution) error {
	w := a.takeWaiter(cmd.GetRequestId())
	if w == nil {
		// The Job already ended, or this Core process is replaying. Dropping it
		// is correct: nothing is waiting.
		return nil
	}
	w.approved <- mcp.Decision{Approved: cmd.GetApproved(), Reason: cmd.GetNote()}
	return nil
}

// OnUserInputResolution delivers an answer to the blocked question.
func (a *Adapter) OnUserInputResolution(_ context.Context, cmd *backendv1.UserInputResolution) error {
	w := a.takeWaiter(cmd.GetRequestId())
	if w == nil {
		return nil
	}
	w.answer <- cmd.GetValue()
	return nil
}

// OnReconcileInstruction applies a Core reconciliation decision.
func (a *Adapter) OnReconcileInstruction(ctx context.Context, cmd *backendv1.ReconcileInstruction) error {
	switch cmd.GetAction() {
	case backendv1.ReconcileAction_RECONCILE_ACTION_REPLAY_EVENTS:
		// The SDK already replays everything Core has not acknowledged on every
		// reconnection, so there is nothing more to do here.
		a.logger.Info("core asked for an event replay",
			slog.String("jobId", cmd.GetJobId()),
			slog.Uint64("fromSequence", cmd.GetFromBackendSequence()))
		return nil

	case backendv1.ReconcileAction_RECONCILE_ACTION_REISSUE_CANCEL:
		return a.OnCancelJob(ctx, &backendv1.CancelJob{
			RunId: cmd.GetRunId(), JobId: cmd.GetJobId(), Reason: "reissued after reconnection",
		})

	case backendv1.ReconcileAction_RECONCILE_ACTION_ABANDON_JOB:
		a.releaseJob(cmd.GetJobId(), ErrJobAbandoned)
		return a.runner.Cancel(cmd.GetJobId())

	default:
		return nil
	}
}

// updateActiveRuns reports how many Jobs are live, so Core sees real capacity.
func (a *Adapter) updateActiveRuns() {
	a.mu.Lock()
	active := int32(len(a.jobs))
	a.mu.Unlock()

	if sdk := a.sdk(); sdk != nil {
		sdk.SetActiveRuns(active)
	}
}

// takeWaiter removes and returns a pending request.
func (a *Adapter) takeWaiter(requestID string) *waiter {
	a.mu.Lock()
	defer a.mu.Unlock()

	w, ok := a.waiters[requestID]
	if !ok {
		return nil
	}
	delete(a.waiters, requestID)
	if job, ok := a.jobs[w.jobID]; ok {
		delete(job.requests, requestID)
	}
	return w
}

// releaseJob forgets a Job and fails anything still waiting on it, so a
// cancelled or finished Job never leaves a blocked tool call behind.
func (a *Adapter) releaseJob(jobID string, cause error) {
	a.mu.Lock()
	job, ok := a.jobs[jobID]
	delete(a.jobs, jobID)

	var orphaned []*waiter
	if ok {
		for requestID := range job.requests {
			if w, exists := a.waiters[requestID]; exists {
				orphaned = append(orphaned, w)
				delete(a.waiters, requestID)
			}
		}
	}
	a.mu.Unlock()

	for _, w := range orphaned {
		w.failed <- cause
	}
}

// emptyStruct is the payload of a request with nothing structured to carry.
func emptyStruct() *structpb.Struct {
	value, _ := structpb.NewStruct(map[string]any{})
	return value
}

// coreTools translates what Core declared into what the local tool endpoint
// exposes. The backend forwards names and schemas it does not interpret, which
// is what lets Core add a tool without a backend release.
func coreTools(pc *backendv1.ProjectContext) []mcp.CoreTool {
	specs := pc.GetTools()
	out := make([]mcp.CoreTool, 0, len(specs))
	for _, spec := range specs {
		tool := mcp.CoreTool{Name: spec.GetName(), Description: spec.GetDescription()}
		if schema := spec.GetInputSchema(); schema != nil {
			tool.InputSchema = schema.AsMap()
		}
		out = append(out, tool)
	}
	return out
}
