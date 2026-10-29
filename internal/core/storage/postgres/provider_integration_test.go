package postgres_test

import (
	"slices"
	"testing"
	"time"

	"github.com/rclsilver/threavia/internal/core/domain"
)

// TestProviderRecoverySkipsRunsHoldingAnActiveJob pins which Jobs the periodic
// recovery sweep offers for dispatch. A Job queued behind an active Job of its
// Run can never take the slot: offering it only trips
// jobs_run_single_active_key, which PostgreSQL logs as an error on every sweep.
// The Job that ends starts the next one. A queued Job of an idle Run, and a
// due provider wait, must still be offered: that is how work resumes after
// Core restarts.
func TestProviderRecoverySkipsRunsHoldingAnActiveJob(t *testing.T) {
	store, ctx := newTestStore(t)

	idle := newFixture(t, store, ctx)

	busy := newFixture(t, store, ctx)
	if _, err := store.TransitionJob(ctx, busy.job.ID, domain.JobQueued, domain.JobRunning, nil); err != nil {
		t.Fatalf("starting the busy run's job: %v", err)
	}
	followUp := domain.Job{ID: domain.NewJobID(), RunID: busy.run.ID, Status: domain.JobQueued}
	if err := store.CreateJob(ctx, &followUp); err != nil {
		t.Fatalf("queueing a follow-up: %v", err)
	}

	// A due provider wait holds the slot too: the waiting Job is retried, the
	// one queued behind it is not.
	waiting := newFixture(t, store, ctx)
	if _, err := store.TransitionJob(ctx, waiting.job.ID, domain.JobQueued, domain.JobRunning, nil); err != nil {
		t.Fatalf("starting the waiting run's job: %v", err)
	}
	if _, err := store.TransitionJob(ctx, waiting.job.ID, domain.JobRunning, domain.JobWaitingBackend, nil); err != nil {
		t.Fatalf("parking the job: %v", err)
	}
	due := time.Now().Add(-time.Minute)
	if err := store.SetJobBackendWait(ctx, waiting.job.ID, &domain.BackendWait{Reason: "QUOTA_EXCEEDED", RetryAt: &due}, 0); err != nil {
		t.Fatalf("recording the provider wait: %v", err)
	}
	behindWait := domain.Job{ID: domain.NewJobID(), RunID: waiting.run.ID, Status: domain.JobQueued}
	if err := store.CreateJob(ctx, &behindWait); err != nil {
		t.Fatalf("queueing behind the wait: %v", err)
	}

	recoverable := func() []domain.JobID {
		t.Helper()
		ids, err := store.ProviderRecoveryJobs(ctx, time.Now())
		if err != nil {
			t.Fatalf("reading the recovery jobs: %v", err)
		}
		return ids
	}

	if got, want := recoverable(), []domain.JobID{idle.job.ID, waiting.job.ID}; !slices.Equal(got, want) {
		t.Fatalf("recovery jobs = %v, want the idle run's job and the due wait %v", got, want)
	}

	// Once the active Job ends, its follow-up is the Run's next Job.
	if _, err := store.TransitionJob(ctx, busy.job.ID, domain.JobRunning, domain.JobCompleted, nil); err != nil {
		t.Fatalf("completing the busy run's job: %v", err)
	}
	if got := recoverable(); !slices.Contains(got, followUp.ID) || slices.Contains(got, behindWait.ID) {
		t.Fatalf("recovery jobs = %v, want the freed follow-up %s and not %s", got, followUp.ID, behindWait.ID)
	}
}
