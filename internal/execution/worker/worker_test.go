package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/backends/shared/mcp"
	"github.com/rclsilver/threavia/internal/backends/shared/policy"
	"github.com/rclsilver/threavia/internal/backends/shared/runner"
	"github.com/rclsilver/threavia/internal/backends/shared/workspace"
	"github.com/rclsilver/threavia/internal/execution/bridge"
	"github.com/rclsilver/threavia/internal/execution/protocol"
	"github.com/rclsilver/threavia/internal/execution/remote"
	"github.com/rclsilver/threavia/pkg/backend-sdk/quotas"
)

type fakeNative struct {
	asker mcp.Asker
	run   func(context.Context, runner.StartParams, runner.Sink) error
}

func (f *fakeNative) Run(c context.Context, p runner.StartParams, s runner.Sink) error {
	return f.run(c, p, s)
}
func (f *fakeNative) Cancel(string) error               { return nil }
func (f *fakeNative) Inject(string, string, bool) error { return nil }
func (f *fakeNative) Available() error                  { return nil }
func (f *fakeNative) Models(context.Context) (*backendv1.ModelCatalog, error) {
	return &backendv1.ModelCatalog{}, nil
}
func (f *fakeNative) Authenticated(context.Context) (bool, bool) { return true, true }
func (f *fakeNative) Quotas(context.Context) quotas.Status {
	return quotas.Status{Availability: "UNSUPPORTED"}
}

type testSession struct {
	net.Conn
	stopEntered, release chan struct{}
	cancel               context.CancelFunc
	once                 sync.Once
}

func (s *testSession) Stop(ctx context.Context) error {
	s.once.Do(func() { close(s.stopEntered) })
	select {
	case <-s.release:
		s.cancel()
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type testExecutor struct {
	t       *testing.T
	factory Factory
	opened  chan *testSession
}

func (e *testExecutor) OpenJob(ctx context.Context, p runner.StartParams) (remote.Session, error) {
	a, b := net.Pipe()
	workerCtx, cancel := context.WithCancel(context.Background())
	s := &testSession{Conn: a, stopEntered: make(chan struct{}), release: make(chan struct{}), cancel: cancel}
	e.opened <- s
	e.t.Cleanup(func() { cancel(); _ = a.Close(); _ = b.Close() })
	go func() {
		_ = serve(workerCtx, b, Options{Provider: "codex", RunID: p.RunID, JobID: p.JobID}, slog.New(slog.NewTextHandler(io.Discard, nil)), e.factory)
	}()
	return s, nil
}
func (e *testExecutor) Probe(context.Context) (protocol.Status, error) {
	return protocol.Status{Available: true, Authenticated: true, AuthKnown: true}, nil
}

type recordingAsker struct {
	mu    sync.Mutex
	job   string
	calls int
}

func (a *recordingAsker) AskPermission(_ context.Context, job, _ string, _ map[string]any) (mcp.Decision, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.job = job
	return mcp.Decision{Approved: true}, nil
}
func (a *recordingAsker) AskValidation(c context.Context, job, name string, input map[string]any) (mcp.Decision, error) {
	return a.AskPermission(c, job, name, input)
}
func (a *recordingAsker) AskUser(_ context.Context, job, _ string, _ []string, _ bool) (string, error) {
	a.mu.Lock()
	a.job = job
	a.mu.Unlock()
	return "answer", nil
}
func (a *recordingAsker) CallCoreTool(_ context.Context, job, name string, input map[string]any, file string) (map[string]any, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.job = job
	a.calls++
	return map[string]any{"fileField": file}, nil
}

type recordingSink struct {
	mu                 sync.Mutex
	completed, started bool
	message            string
}

func (s *recordingSink) JobStarted(context.Context, string, string) error {
	s.mu.Lock()
	s.started = true
	s.mu.Unlock()
	return nil
}
func (s *recordingSink) NativeSessionBound(context.Context, string, string, string) error { return nil }
func (s *recordingSink) AgentMessage(_ context.Context, _, _, text string) error {
	s.mu.Lock()
	s.message = text
	s.mu.Unlock()
	return nil
}
func (s *recordingSink) ToolStarted(context.Context, string, string, string, string, map[string]any) error {
	return nil
}
func (s *recordingSink) ToolCompleted(context.Context, string, string, string, string, map[string]any) error {
	return nil
}
func (s *recordingSink) ToolFailed(context.Context, string, string, string, string, string) error {
	return nil
}
func (s *recordingSink) WorkspaceChanged(context.Context, string, string, workspace.Summary) error {
	return nil
}
func (s *recordingSink) JobCompleted(context.Context, string, string, string, *backendv1.Usage) error {
	s.mu.Lock()
	s.completed = true
	s.mu.Unlock()
	return nil
}
func (s *recordingSink) JobFailed(_ context.Context, _, _, code, message string, _ *backendv1.Usage) error {
	return errors.New(code + ": " + message)
}
func (s *recordingSink) JobCancelled(context.Context, string, string) error       { return nil }
func (s *recordingSink) Progress(context.Context, string, string, string, string) {}

func TestWorkerRoundTripKeepsToolsScopedAndWaitsForContainerStop(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	localCalls := 0
	executor := &testExecutor{t: t, opened: make(chan *testSession, 1)}
	executor.factory = func(_ *mcp.Server, asker mcp.Asker) (native, error) {
		return &fakeNative{asker: asker, run: func(ctx context.Context, p runner.StartParams, s runner.Sink) error {
			if err := s.JobStarted(ctx, p.RunID, p.JobID); err != nil {
				return err
			}
			decision, err := asker.AskValidation(ctx, p.JobID, "Bash", map[string]any{"command": "read"})
			if err != nil || !decision.Approved {
				return errors.New("approval callback failed")
			}
			if _, err = asker.CallCoreTool(ctx, p.JobID, "artifact", map[string]any{"path": "report"}, "forged"); err == nil {
				return errors.New("worker changed the trusted file field")
			}
			if _, err = asker.CallCoreTool(ctx, p.JobID, "not-registered", nil, ""); err == nil {
				return errors.New("unregistered tool accepted")
			}
			if _, err = asker.CallCoreTool(ctx, p.JobID, "artifact", map[string]any{"path": "report"}, "path"); err != nil {
				return err
			}
			if _, err = asker.AskUser(ctx, "other-job", "not allowed", nil, true); err == nil {
				return errors.New("cross-job asker call accepted")
			}
			answer, err := asker.AskUser(ctx, p.JobID, "continue", nil, true)
			if err != nil || answer != "answer" {
				return errors.New("question callback failed")
			}
			if len(p.ExternalTools) != 1 {
				return errors.New("local tool declarations lost")
			}
			result, err := p.ExternalTools[0].Call(ctx, map[string]any{"x": "y"})
			if err != nil || result.(map[string]any)["ok"] != true {
				return errors.New("local tool callback failed")
			}
			if err = s.AgentMessage(ctx, p.RunID, p.JobID, "done"); err != nil {
				return err
			}
			return s.JobCompleted(ctx, p.RunID, p.JobID, "done", nil)
		}}, nil
	}
	controller := remote.New(executor, slog.New(slog.NewTextHandler(io.Discard, nil)))
	asker := &recordingAsker{}
	controller.SetAsker(asker)
	sink := &recordingSink{}
	params := runner.StartParams{RunID: "run", JobID: "job", CoreTools: []mcp.CoreTool{{Name: "artifact", FileInput: "path"}}, ExternalTools: []mcp.LocalTool{{Name: "local", Call: func(context.Context, map[string]any) (any, error) {
		localCalls++
		return map[string]any{"ok": true}, nil
	}}}}
	done := make(chan error, 1)
	go func() { done <- controller.Run(ctx, params, sink) }()
	var session *testSession
	select {
	case session = <-executor.opened:
	case <-ctx.Done():
		t.Fatal("worker was not opened")
	}
	select {
	case <-session.stopEntered:
	case <-ctx.Done():
		t.Fatal("worker did not reach container stop")
	}
	sink.mu.Lock()
	premature := sink.completed
	sink.mu.Unlock()
	if premature {
		t.Fatal("completion preceded container stop confirmation")
	}
	close(session.release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("remote runner stuck")
	}
	sink.mu.Lock()
	completed, started, message := sink.completed, sink.started, sink.message
	sink.mu.Unlock()
	if !completed || !started || message != "done" || localCalls != 1 {
		t.Fatalf("lost work: completed=%v started=%v message=%q local=%d", completed, started, message, localCalls)
	}
	asker.mu.Lock()
	defer asker.mu.Unlock()
	if asker.job != "job" || asker.calls != 1 {
		t.Fatalf("callbacks were not scoped: %+v", asker)
	}
}

func TestProbeWorkerRefusesJobExecution(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	factory := func(_ *mcp.Server, a mcp.Asker) (native, error) {
		return &fakeNative{asker: a, run: func(context.Context, runner.StartParams, runner.Sink) error { return errors.New("must not run") }}, nil
	}
	go func() {
		_ = serve(ctx, b, Options{Probe: true}, slog.New(slog.NewTextHandler(io.Discard, nil)), factory)
	}()
	peer := bridge.New(ctx, a, nil)
	defer peer.Close()
	if err := peer.Call(ctx, "run", protocol.Start{Version: protocol.Version}, nil); err == nil {
		t.Fatal("probe accepted a job")
	}
	var status protocol.Status
	if err := peer.Call(ctx, "status", nil, &status); err != nil || !status.Available || !status.Authenticated {
		t.Fatalf("probe failed: %v %+v", err, status)
	}
}

func TestPolicyChangeConfirmsOldContainerBeforeResumingNativeSession(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	executor := &testExecutor{t: t, opened: make(chan *testSession, 2)}
	paramsSeen := make(chan runner.StartParams, 2)
	actionObserved := make(chan struct{})
	executor.factory = func(_ *mcp.Server, a mcp.Asker) (native, error) {
		return &fakeNative{asker: a, run: func(ctx context.Context, p runner.StartParams, s runner.Sink) error {
			paramsSeen <- p
			if err := s.JobStarted(ctx, p.RunID, p.JobID); err != nil {
				return err
			}
			if err := s.NativeSessionBound(ctx, p.RunID, p.JobID, "native-session"); err != nil {
				return err
			}
			if !p.Policy.AllowFilesystemWrite {
				if err := s.ToolStarted(ctx, p.RunID, p.JobID, "call", "Read", nil); err != nil {
					return err
				}
				close(actionObserved)
				<-ctx.Done()
				return s.JobCancelled(context.WithoutCancel(ctx), p.RunID, p.JobID)
			}
			if p.NativeSessionID != "native-session" || p.ActionsUsed != 1 || p.ActiveSecondsUsed < 1 || p.Prompt == "original prompt" {
				return errors.New("native session or cumulative budget lost")
			}
			return s.JobCompleted(ctx, p.RunID, p.JobID, "continued", nil)
		}}, nil
	}
	r := remote.New(executor, slog.New(slog.NewTextHandler(io.Discard, nil)))
	sink := &recordingSink{}
	done := make(chan error, 1)
	go func() {
		done <- r.Run(ctx, runner.StartParams{RunID: "run", JobID: "job", Prompt: "original prompt"}, sink)
	}()
	first := <-executor.opened
	select {
	case <-actionObserved:
	case <-ctx.Done():
		t.Fatal("first attempt did not run")
	}
	r.UpdatePolicy("job", policy.Policy{AllowFilesystemWrite: true})
	select {
	case <-first.stopEntered:
	case <-ctx.Done():
		t.Fatal("policy update did not stop old container")
	}
	select {
	case <-executor.opened:
		t.Fatal("replacement started before old container stopped")
	default:
	}
	close(first.release)
	var second *testSession
	select {
	case second = <-executor.opened:
	case <-ctx.Done():
		t.Fatal("replacement not opened")
	}
	select {
	case <-second.stopEntered:
	case <-ctx.Done():
		t.Fatal("replacement did not complete")
	}
	close(second.release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("policy resume stuck")
	}
	firstParams, secondParams := <-paramsSeen, <-paramsSeen
	if firstParams.Policy.AllowFilesystemWrite || !secondParams.Policy.AllowFilesystemWrite {
		t.Fatal("pod and provider used inconsistent policies")
	}
	sink.mu.Lock()
	completed := sink.completed
	sink.mu.Unlock()
	if !completed {
		t.Fatal("native session continuation did not complete")
	}
}
