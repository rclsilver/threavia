package adapter_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/backends/shared/adapter"
	"github.com/rclsilver/threavia/internal/backends/shared/runner"
	"github.com/rclsilver/threavia/pkg/backend-sdk/client"
	"github.com/rclsilver/threavia/pkg/backend-sdk/state"
)

// captureRunner records the parameters it was started with.
type captureRunner struct {
	busyRunner
	params chan runner.StartParams
}

func (r captureRunner) Run(_ context.Context, p runner.StartParams, _ runner.Sink) error {
	r.params <- p
	<-r.release
	return nil
}

// TestDecisionsReachTheRunner pins that the decisions in the project context
// are handed to the runner, which is what puts them in the agent's prompt.
func TestDecisionsReachTheRunner(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := state.NewMemoryStore()
	capture := captureRunner{busyRunner: busyRunner{release: make(chan struct{})}, params: make(chan runner.StartParams, 1)}
	t.Cleanup(func() { close(capture.release) })

	a := adapter.New(adapter.Config{}, capture, store, logger)
	cfg := client.DefaultConfig()
	cfg.CoreAddress = "core.invalid:9090"
	cfg.Token = "credential"
	cfg.InstanceName = "laptop"
	sdk, err := client.New(cfg, a, store, logger)
	if err != nil {
		t.Fatalf("building the sdk client: %v", err)
	}
	a.Bind(sdk)

	if err := a.OnStartJob(context.Background(), &backendv1.StartJob{
		RunId: "run-1", JobId: "job-1", Prompt: "go",
		ProjectContext: &backendv1.ProjectContext{
			WorkingDirectoryPath: t.TempDir(),
			Decisions: []*backendv1.ContextDecision{
				{Id: "g", Title: "[Core memory] Global", Content: "Applies everywhere."},
				{Id: "p", Title: "Local", Content: "Applies here."},
			},
		},
		ExecutionPolicy: &backendv1.ExecutionPolicy{Mode: backendv1.ExecutionMode_EXECUTION_MODE_INTERACTIVE},
	}); err != nil {
		t.Fatalf("starting the job: %v", err)
	}
	select {
	case p := <-capture.params:
		if len(p.Decisions) != 2 || p.Decisions[0].GetId() != "g" || p.Decisions[1].GetContent() != "Applies here." {
			t.Fatalf("decisions = %v, want the two sent by Core in order", p.Decisions)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the job never started")
	}
}

// busyRunner holds a Job open until the test ends, which is what a Job looks
// like while the user changes its policy.
type busyRunner struct {
	started chan struct{}
	release chan struct{}
}

func (r busyRunner) Run(context.Context, runner.StartParams, runner.Sink) error {
	r.started <- struct{}{}
	<-r.release
	return nil
}
func (busyRunner) Cancel(string) error               { return nil }
func (busyRunner) Inject(string, string, bool) error { return nil }
func (busyRunner) Available() error                  { return nil }

// TestAPolicyChangeReachesTheRunningJob is a regression test: the policy used
// to be fixed when a Job started, so switching a Session to a looser mode kept
// the running Job asking for everything until it ended.
func TestAPolicyChangeReachesTheRunningJob(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := state.NewMemoryStore()
	busy := busyRunner{started: make(chan struct{}, 1), release: make(chan struct{})}
	t.Cleanup(func() { close(busy.release) })

	a := adapter.New(adapter.Config{}, busy, store, logger)
	cfg := client.DefaultConfig()
	cfg.CoreAddress = "core.invalid:9090"
	cfg.Token = "credential"
	cfg.InstanceName = "laptop"
	sdk, err := client.New(cfg, a, store, logger)
	if err != nil {
		t.Fatalf("building the sdk client: %v", err)
	}
	a.Bind(sdk)

	ctx := context.Background()
	if err := a.OnStartJob(ctx, &backendv1.StartJob{
		RunId: "run-1", JobId: "job-1", Prompt: "écris le fichier",
		ProjectContext:  &backendv1.ProjectContext{WorkingDirectoryPath: t.TempDir()},
		ExecutionPolicy: &backendv1.ExecutionPolicy{Mode: backendv1.ExecutionMode_EXECUTION_MODE_INTERACTIVE},
	}); err != nil {
		t.Fatalf("starting the job: %v", err)
	}
	select {
	case <-busy.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the job never started")
	}

	// Writing is forbidden: the gate answers without asking anyone.
	decision, err := a.AskPermission(ctx, "job-1", "Write", map[string]any{"file_path": "x"})
	if err != nil || decision.Approved {
		t.Fatalf("before the change: %+v, %v; want a refusal", decision, err)
	}

	if err := a.OnUpdateJobPolicy(ctx, &backendv1.UpdateJobPolicy{
		RunId: "run-1", JobId: "job-1",
		ExecutionPolicy: &backendv1.ExecutionPolicy{
			Mode:                 backendv1.ExecutionMode_EXECUTION_MODE_AUTONOMOUS,
			AllowFilesystemWrite: true,
		},
	}); err != nil {
		t.Fatalf("updating the policy: %v", err)
	}

	decision, err = a.AskPermission(ctx, "job-1", "Write", map[string]any{"file_path": "x"})
	if err != nil || !decision.Approved {
		t.Fatalf("after the change: %+v, %v; want the write allowed", decision, err)
	}
}
