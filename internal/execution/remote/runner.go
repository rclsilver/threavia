// Package remote adapts the backend's existing runner contract to one remote
// worker per Job. Only the backend owns the Core and executor credentials.
package remote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"reflect"
	"sync"
	"time"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/backends/shared/mcp"
	"github.com/rclsilver/threavia/internal/backends/shared/policy"
	"github.com/rclsilver/threavia/internal/backends/shared/runner"
	"github.com/rclsilver/threavia/internal/execution/bridge"
	"github.com/rclsilver/threavia/internal/execution/protocol"
	"github.com/rclsilver/threavia/pkg/backend-sdk/quotas"
)

// Session.Stop must confirm that the execution container stopped. A Job's
// terminal event is deliberately retained until that confirmation.
type Session interface {
	io.ReadWriteCloser
	Stop(context.Context) error
}
type Executor interface {
	OpenJob(context.Context, runner.StartParams) (Session, error)
	Probe(context.Context) (protocol.Status, error)
}
type execution struct {
	mu            sync.Mutex
	peer          *bridge.Peer
	policy        policy.Policy
	terminal      *protocol.Event
	finished      bool
	revision      uint64
	cancelJob     context.CancelFunc
	cancelAttempt context.CancelFunc
	nativeID      string
	actions       uint32
	started       bool
}
type Runner struct {
	executor Executor
	logger   *slog.Logger
	mu       sync.Mutex
	asker    mcp.Asker
	jobs     map[string]*execution
	pending  map[string]policy.Policy
	probeMu  sync.Mutex
	probe    protocol.Status
	probeErr error
	probed   time.Time
}

func New(executor Executor, logger *slog.Logger) *Runner {
	return &Runner{executor: executor, logger: logger, jobs: map[string]*execution{}, pending: map[string]policy.Policy{}, probeErr: errors.New("execution profile has not been probed")}
}
func (r *Runner) SetAsker(asker mcp.Asker) { r.mu.Lock(); r.asker = asker; r.mu.Unlock() }
func (r *Runner) Available() error {
	if checker, ok := r.executor.(interface{ Available() error }); ok {
		if err := checker.Available(); err != nil {
			return err
		}
	}
	r.probeMu.Lock()
	defer r.probeMu.Unlock()
	return r.probeErr
}
func (r *Runner) status(ctx context.Context) (protocol.Status, error) {
	r.probeMu.Lock()
	defer r.probeMu.Unlock()
	if time.Since(r.probed) < 30*time.Second {
		return r.probe, r.probeErr
	}
	result, err := r.executor.Probe(ctx)
	if err == nil && !result.Available {
		err = fmt.Errorf("provider unavailable in execution profile: %s", result.Error)
	}
	r.probe, r.probeErr, r.probed = result, err, time.Now()
	return result, err
}
func (r *Runner) Models(ctx context.Context) (*backendv1.ModelCatalog, error) {
	s, err := r.status(ctx)
	if err != nil {
		return nil, err
	}
	if s.ModelError != "" {
		return nil, errors.New(s.ModelError)
	}
	return s.Models, nil
}
func (r *Runner) Authenticated(ctx context.Context) (bool, bool) {
	s, err := r.status(ctx)
	return s.Authenticated, err == nil && s.AuthKnown
}
func (r *Runner) Quotas(ctx context.Context) quotas.Status {
	s, err := r.status(ctx)
	if err != nil {
		return quotas.Unavailable("Kubernetes provider probe is unavailable.")
	}
	return s.Quotas
}
func (r *Runner) ValidatePolicy(p policy.Policy) error { return nil }
func (r *Runner) UpdatePolicy(job string, p policy.Policy) {
	r.mu.Lock()
	live := r.jobs[job]
	if live == nil {
		r.pending[job] = p
	}
	r.mu.Unlock()
	if live == nil {
		return
	}
	live.mu.Lock()
	if reflect.DeepEqual(live.policy, p) {
		live.mu.Unlock()
		return
	}
	live.policy = p
	live.revision++
	cancel := live.cancelAttempt
	live.mu.Unlock()
	// Pod mounts and network policy are immutable for this attempt. Stop it,
	// confirm its container exit, then resume the native session in a new pod.
	if cancel != nil {
		cancel()
	}
}
func (r *Runner) Inject(job, text string, interrupt bool) error {
	peer, err := r.peer(job)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return peer.Call(ctx, "inject", protocol.Input{Text: text, Interrupt: interrupt}, nil)
}
func (r *Runner) Cancel(job string) error {
	r.mu.Lock()
	live := r.jobs[job]
	r.mu.Unlock()
	if live == nil {
		return runner.ErrUnknownJob
	}
	live.mu.Lock()
	cancel := live.cancelJob
	live.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}
func (r *Runner) peer(job string) (*bridge.Peer, error) {
	r.mu.Lock()
	live := r.jobs[job]
	r.mu.Unlock()
	if live == nil {
		return nil, runner.ErrUnknownJob
	}
	live.mu.Lock()
	defer live.mu.Unlock()
	if live.finished || live.peer == nil {
		return nil, errors.New("execution worker is not ready")
	}
	return live.peer, nil
}
func (r *Runner) Run(ctx context.Context, p runner.StartParams, sink runner.Sink) error {
	ctx, cancelJob := context.WithCancel(ctx)
	defer cancelJob()
	live := &execution{policy: p.Policy, cancelJob: cancelJob}
	r.mu.Lock()
	if r.jobs[p.JobID] != nil {
		r.mu.Unlock()
		return errors.New("job already has a worker")
	}
	r.jobs[p.JobID] = live
	if updated, ok := r.pending[p.JobID]; ok {
		live.policy = updated
		delete(r.pending, p.JobID)
	}
	asker := r.asker
	r.mu.Unlock()
	defer func() { r.mu.Lock(); delete(r.jobs, p.JobID); r.mu.Unlock() }()
	reportCtx := context.WithoutCancel(ctx)
	started := false
	for {
		live.mu.Lock()
		p.Policy = live.policy
		revision := live.revision
		attemptCtx, cancel := context.WithCancel(ctx)
		live.cancelAttempt = cancel
		live.mu.Unlock()
		attempt := &execution{started: started, nativeID: p.NativeSessionID}
		elapsed, runErr := r.attempt(attemptCtx, p, sink, asker, live, attempt)
		cancel()
		attempt.mu.Lock()
		terminal, nativeID, actions, wasStarted := attempt.terminal, attempt.nativeID, attempt.actions, attempt.started
		attempt.finished = true
		attempt.mu.Unlock()
		live.mu.Lock()
		changed := live.revision != revision
		live.peer = nil
		live.cancelAttempt = nil
		live.mu.Unlock()
		if ctx.Err() != nil && (terminal == nil || terminal.Kind != "completed") {
			return sink.JobCancelled(reportCtx, p.RunID, p.JobID)
		}
		if changed && (terminal == nil || terminal.Kind == "cancelled" || terminal.Kind == "failed") {
			if nativeID == "" && (actions > 0 || wasStarted) {
				return sink.JobFailed(reportCtx, p.RunID, p.JobID, "POLICY_RESUME_UNAVAILABLE", "Policy changed, but the stopped worker did not bind a native session. Start a new Job to continue.", nil)
			}
			p.NativeSessionID = nativeID
			if nativeID != "" {
				p.Prompt = "Continue the interrupted Job under the updated execution policy. Inspect the existing session and workspace before acting; preserve completed work and do not repeat commands already performed."
				p.Attachments = nil
			}
			p.ActionsUsed += actions
			p.ActiveSecondsUsed += uint32(math.Ceil(elapsed.Seconds()))
			started = wasStarted
			sink.Progress(reportCtx, p.RunID, p.JobID, "execution_policy_changed", "The previous container stopped. Resuming the native session with the updated policy.")
			continue
		}
		if terminal != nil {
			return deliver(reportCtx, p, sink, *terminal)
		}
		if runErr == nil {
			runErr = errors.New("worker exited without a terminal outcome")
		}
		return sink.JobFailed(reportCtx, p.RunID, p.JobID, "EXECUTOR_CONNECTION_LOST", runErr.Error(), nil)
	}
}

func (r *Runner) attempt(ctx context.Context, p runner.StartParams, sink runner.Sink, asker mcp.Asker, live, attempt *execution) (time.Duration, error) {
	session, err := r.executor.OpenJob(ctx, p)
	if err != nil {
		return 0, err
	}
	defer session.Close()
	reportCtx := context.WithoutCancel(ctx)
	connectionCtx, closeConnection := context.WithCancel(reportCtx)
	defer closeConnection()
	peer := bridge.New(connectionCtx, session, func(callCtx context.Context, method string, raw json.RawMessage) (any, error) {
		return dispatch(callCtx, attempt, p, sink, asker, method, raw)
	})
	defer peer.Close()
	defer protocol.KeepAlive(connectionCtx, peer)()
	live.mu.Lock()
	live.peer = peer
	live.mu.Unlock()
	start := protocol.Start{Version: protocol.Version, Params: p}
	for _, tool := range p.ExternalTools {
		start.Tools = append(start.Tools, protocol.Tool{Name: tool.Name, Description: tool.Description, InputSchema: tool.InputSchema, RawResult: tool.RawResult})
	}
	began := time.Now()
	err = peer.Call(ctx, "run", start, nil)
	elapsed := time.Since(began)
	// Keep the Job and its capacity reserved until stop is confirmed. Returning
	// here would let the adapter later mistake cancellation for an absent Job.
	warned := false
	for {
		stop, cancel := context.WithTimeout(reportCtx, 90*time.Second)
		stopErr := session.Stop(stop)
		cancel()
		if stopErr == nil {
			break
		}
		if !warned {
			sink.Progress(reportCtx, p.RunID, p.JobID, "execution_unconfirmed", "The execution container has not confirmed its stop. This Job remains reserved.")
			warned = true
		}
		timer := time.NewTimer(5 * time.Second)
		<-timer.C
	}
	return elapsed, err
}
func dispatch(ctx context.Context, live *execution, p runner.StartParams, sink runner.Sink, asker mcp.Asker, method string, raw json.RawMessage) (any, error) {
	if method == "event" {
		var event protocol.Event
		if err := json.Unmarshal(raw, &event); err != nil {
			return nil, err
		}
		switch event.Kind {
		case "completed", "failed", "cancelled", "blocked":
			if event.Kind == "blocked" && event.Block == nil {
				return nil, errors.New("provider block is missing")
			}
			live.mu.Lock()
			defer live.mu.Unlock()
			if live.terminal != nil {
				return nil, errors.New("duplicate terminal event")
			}
			live.terminal = &event
			return nil, nil
		}
		live.mu.Lock()
		ended := live.terminal != nil || live.finished
		if !ended {
			switch event.Kind {
			case "started":
				if live.started {
					live.mu.Unlock()
					return nil, nil
				}
				live.started = true
			case "native":
				live.nativeID = event.NativeSessionID
			case "tool_started":
				live.actions++
			}
		}
		live.mu.Unlock()
		if ended {
			return nil, errors.New("event after terminal outcome")
		}
		return nil, deliver(ctx, p, sink, event)
	}
	if asker == nil {
		return nil, errors.New("job tool endpoint is unavailable")
	}
	live.mu.Lock()
	ended := live.terminal != nil || live.finished
	live.mu.Unlock()
	if ended {
		return nil, errors.New("execution no longer accepts tool calls")
	}
	var input protocol.Ask
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, err
	}
	switch method {
	case "permission":
		return asker.AskPermission(ctx, p.JobID, input.Name, input.Input)
	case "validation":
		return asker.AskValidation(ctx, p.JobID, input.Name, input.Input)
	case "question":
		return asker.AskUser(ctx, p.JobID, input.Prompt, input.Choices, input.FreeText)
	case "review":
		if checker, ok := asker.(interface {
			ReviewCheckpoint(context.Context, string) error
		}); ok {
			return nil, checker.ReviewCheckpoint(ctx, p.JobID)
		}
		return nil, errors.New("review checkpoint is unavailable")
	case "core":
		for _, tool := range p.CoreTools {
			if tool.Name == input.Name {
				// The worker cannot invent a file field and make the control backend read
				// a different private path. Use only Core's registered tool declaration.
				if input.FileInput != tool.FileInput {
					return nil, errors.New("tool file contract changed")
				}
				return asker.CallCoreTool(ctx, p.JobID, input.Name, input.Input, tool.FileInput)
			}
		}
		return nil, errors.New("Core tool is not registered for this job")
	case "local":
		for _, tool := range p.ExternalTools {
			if tool.Name == input.Name {
				return tool.Call(ctx, input.Input)
			}
		}
		return nil, errors.New("local tool is not registered for this job")
	default:
		return nil, errors.New("unknown worker callback")
	}
}
func deliver(ctx context.Context, p runner.StartParams, s runner.Sink, e protocol.Event) error {
	run, job := p.RunID, p.JobID
	switch e.Kind {
	case "started":
		return runner.ReportJobStarted(ctx, s, run, job, e.Model, e.Effort)
	case "native":
		return s.NativeSessionBound(ctx, run, job, e.NativeSessionID)
	case "message":
		return s.AgentMessage(ctx, run, job, e.Text)
	case "tool_started":
		return s.ToolStarted(ctx, run, job, e.CallID, e.Name, e.Input)
	case "tool_completed":
		return s.ToolCompleted(ctx, run, job, e.CallID, e.Name, e.Output)
	case "tool_failed":
		return s.ToolFailed(ctx, run, job, e.CallID, e.Name, e.Text)
	case "workspace":
		return s.WorkspaceChanged(ctx, run, job, e.Workspace)
	case "completed":
		return s.JobCompleted(ctx, run, job, e.Text, e.Usage)
	case "failed":
		return s.JobFailed(ctx, run, job, e.Code, e.Text, e.Usage)
	case "cancelled":
		return s.JobCancelled(ctx, run, job)
	case "blocked":
		return runner.ReportProviderBlock(ctx, s, run, job, e.Block, e.Usage)
	case "quotas":
		return runner.ReportQuotas(ctx, s, e.Quotas, e.Complete)
	case "progress":
		s.Progress(ctx, run, job, e.Name, e.Text)
		return nil
	default:
		return errors.New("unknown execution event")
	}
}
