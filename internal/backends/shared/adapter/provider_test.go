package adapter_test

import (
	"context"
	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/backends/shared/runner"
	"testing"
	"time"
)

func TestStoppedProviderAttemptIsDurableAndCancellable(t *testing.T) {
	a, store := boundAdapter(t)
	ctx := context.Background()
	// The SDK is deliberately disconnected: status sends fail, while negotiated
	// capability and durable job events remain available locally.
	_ = a.OnConnected(ctx, &backendv1.Welcome{ProviderRecovery: true})
	if err := a.ProviderBlocked(ctx, "run", "job", &runner.ProviderBlock{Reason: "QUOTA_EXCEEDED", Message: "quota exhausted", ActiveSeconds: 3, InputReceipt: "confirmed-input"}, &backendv1.Usage{InputTokens: 7}); err != nil {
		t.Fatal(err)
	}
	pending, err := store.PendingEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].GetJobWaitingBackend() == nil {
		t.Fatal(pending)
	}
	wait := pending[0].GetJobWaitingBackend()
	if wait.ActiveSeconds != 3 || wait.GetUsage().InputTokens != 7 || wait.InputReceipt != "confirmed-input" || !wait.RetryAt.AsTime().After(time.Now().Add(4*time.Minute)) {
		t.Fatal(wait)
	}
	jobs, err := store.Jobs(ctx)
	if err != nil || len(jobs) != 1 || jobs[0].Status != backendv1.JobStatus_JOB_STATUS_WAITING_BACKEND {
		t.Fatal(jobs, err)
	}
	if err := a.OnCancelJob(ctx, &backendv1.CancelJob{RunId: "run", JobId: "job"}); err != nil {
		t.Fatal(err)
	}
	pending, _ = store.PendingEvents(ctx)
	if len(pending) != 2 || pending[1].GetJobCancelled() == nil {
		t.Fatal("stopped provider job could not be cancelled", pending)
	}
}

func TestOldCoreReceivesTerminalProviderFailure(t *testing.T) {
	a, store := boundAdapter(t)
	ctx := context.Background()
	_ = a.OnConnected(ctx, &backendv1.Welcome{})
	if err := a.ProviderBlocked(ctx, "run", "job", &runner.ProviderBlock{Reason: "AUTHENTICATION_REQUIRED", Message: "Login required"}, nil); err != nil {
		t.Fatal(err)
	}
	pending, err := store.PendingEvents(ctx)
	if err != nil || len(pending) != 1 || pending[0].GetJobFailed() == nil {
		t.Fatal(pending, err)
	}
	jobs, err := store.Jobs(ctx)
	if err != nil || len(jobs) != 1 || jobs[0].Status != backendv1.JobStatus_JOB_STATUS_FAILED {
		t.Fatal(jobs, err)
	}
}

func TestExplicitQuotaRetryHintIsNotReplacedByFallback(t *testing.T) {
	a, store := boundAdapter(t)
	ctx := context.Background()
	_ = a.OnConnected(ctx, &backendv1.Welcome{ProviderRecovery: true})
	at := time.Now().UTC().Add(2 * time.Minute)
	if err := a.ProviderBlocked(ctx, "run", "job", &runner.ProviderBlock{Reason: "QUOTA_EXCEEDED", RetryAt: &at}, nil); err != nil {
		t.Fatal(err)
	}
	pending, err := store.PendingEvents(ctx)
	if err != nil || len(pending) != 1 || !pending[0].GetJobWaitingBackend().GetRetryAt().AsTime().Equal(at) {
		t.Fatal(pending, err)
	}
}
