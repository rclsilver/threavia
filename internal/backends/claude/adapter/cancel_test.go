package adapter_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/backends/claude/adapter"
	"github.com/rclsilver/threavia/internal/backends/claude/runner"
	"github.com/rclsilver/threavia/pkg/backend-sdk/client"
	"github.com/rclsilver/threavia/pkg/backend-sdk/state"
)

// idleRunner owns no process, which is what a backend looks like after a
// restart: Core may still believe one of its Jobs is live.
type idleRunner struct{}

func (idleRunner) Run(context.Context, runner.StartParams, runner.Sink) error { return nil }
func (idleRunner) Cancel(string) error                                        { return runner.ErrUnknownJob }
func (idleRunner) Available() error                                           { return nil }

// boundAdapter returns an adapter wired to an unreachable Core, so the events it
// reports are buffered in its own state rather than lost.
func boundAdapter(t *testing.T) (*adapter.Adapter, state.Store) {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := state.NewMemoryStore()
	a := adapter.New(adapter.Config{}, idleRunner{}, store, logger)

	cfg := client.DefaultConfig()
	cfg.CoreAddress = "core.invalid:9090"
	cfg.Token = "credential"
	cfg.InstanceName = "laptop"
	sdk, err := client.New(cfg, a, store, logger)
	if err != nil {
		t.Fatalf("building the sdk client: %v", err)
	}
	a.Bind(sdk)
	return a, store
}

// TestCancellingAJobNothingRunsReportsItStopped is a regression test.
//
// Core waits for the backend to confirm a stop, and reissues the cancel at
// every reconnection until it comes. A backend that answered nothing to a Job
// no process owned left the Job CANCELLING for good, with the rest of the Run
// queued behind it.
func TestCancellingAJobNothingRunsReportsItStopped(t *testing.T) {
	a, store := boundAdapter(t)
	ctx := context.Background()

	if err := a.OnCancelJob(ctx, &backendv1.CancelJob{
		RunId: "run-1", JobId: "job-1", Reason: "reissued after reconnection",
	}); err != nil {
		t.Fatalf("cancelling a job nothing runs: %v", err)
	}

	pending, err := store.PendingEvents(ctx)
	if err != nil {
		t.Fatalf("reading the buffered events: %v", err)
	}
	if len(pending) != 1 || pending[0].GetJobCancelled() == nil {
		t.Fatalf("buffered %d events, want one job cancelled: %+v", len(pending), pending)
	}

	jobs, err := store.Jobs(ctx)
	if err != nil {
		t.Fatalf("reading the local job state: %v", err)
	}
	if len(jobs) != 1 || jobs[0].Status != backendv1.JobStatus_JOB_STATUS_CANCELLED {
		t.Fatalf("local state = %+v, want the job recorded as cancelled", jobs)
	}
}

// TestCancellingAJobThatAlreadyEndedKeepsItsOutcome pins the other half: a
// cancel that arrives after the work finished must not rewrite what happened.
func TestCancellingAJobThatAlreadyEndedKeepsItsOutcome(t *testing.T) {
	a, store := boundAdapter(t)
	ctx := context.Background()

	if err := store.SaveJob(ctx, state.JobRecord{
		JobID: "job-1", RunID: "run-1", Status: backendv1.JobStatus_JOB_STATUS_COMPLETED,
	}); err != nil {
		t.Fatalf("recording the finished job: %v", err)
	}

	if err := a.OnCancelJob(ctx, &backendv1.CancelJob{
		RunId: "run-1", JobId: "job-1", Reason: "too late",
	}); err != nil {
		t.Fatalf("cancelling a finished job: %v", err)
	}

	pending, err := store.PendingEvents(ctx)
	if err != nil {
		t.Fatalf("reading the buffered events: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("buffered %d events, want none: %+v", len(pending), pending)
	}

	jobs, err := store.Jobs(ctx)
	if err != nil {
		t.Fatalf("reading the local job state: %v", err)
	}
	if len(jobs) != 1 || jobs[0].Status != backendv1.JobStatus_JOB_STATUS_COMPLETED {
		t.Fatalf("local state = %+v, want the job still completed", jobs)
	}
}
