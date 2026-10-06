package state

import (
	"context"
	"testing"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
)

func TestNextSequenceIsMonotonicPerJob(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := NewMemoryStore()

	for i := uint64(1); i <= 3; i++ {
		got, err := store.NextSequence(ctx, "job-a")
		if err != nil {
			t.Fatalf("allocating a sequence: %v", err)
		}
		if got != i {
			t.Fatalf("NextSequence(job-a) = %d, want %d", got, i)
		}
	}

	// Sequences are per Job: unrelated concurrent Jobs are not serialised.
	got, err := store.NextSequence(ctx, "job-b")
	if err != nil {
		t.Fatalf("allocating a sequence: %v", err)
	}
	if got != 1 {
		t.Fatalf("NextSequence(job-b) = %d, want 1", got)
	}
}

// TestPendingEventsSurviveUntilAcknowledged pins the buffering rule of
// specification section 10: an event is dropped only once Core confirms it
// persisted it.
func TestPendingEventsSurviveUntilAcknowledged(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := NewMemoryStore()

	for i := uint64(1); i <= 3; i++ {
		event := &backendv1.JobEvent{JobId: "job-a", RunId: "run-a", BackendSequence: i}
		if err := store.AppendPending(ctx, event); err != nil {
			t.Fatalf("buffering an event: %v", err)
		}
	}

	pending, err := store.PendingEvents(ctx)
	if err != nil {
		t.Fatalf("reading buffered events: %v", err)
	}
	if len(pending) != 3 {
		t.Fatalf("buffered %d events, want 3", len(pending))
	}
	for i, event := range pending {
		if event.GetBackendSequence() != uint64(i+1) {
			t.Fatalf("buffered events are out of order: %d at index %d", event.GetBackendSequence(), i)
		}
	}

	if err := store.Ack(ctx, "job-a", 2); err != nil {
		t.Fatalf("acknowledging events: %v", err)
	}
	pending, err = store.PendingEvents(ctx)
	if err != nil {
		t.Fatalf("reading buffered events: %v", err)
	}
	if len(pending) != 1 || pending[0].GetBackendSequence() != 3 {
		t.Fatalf("after acknowledging through 2, want only sequence 3, got %d events", len(pending))
	}

	if err := store.Ack(ctx, "job-a", 3); err != nil {
		t.Fatalf("acknowledging events: %v", err)
	}
	pending, _ = store.PendingEvents(ctx)
	if len(pending) != 0 {
		t.Fatalf("after acknowledging everything, want no buffered event, got %d", len(pending))
	}
}

// TestAppendPendingCopiesTheEvent keeps a caller from mutating a buffered event
// after handing it over.
func TestAppendPendingCopiesTheEvent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := NewMemoryStore()

	event := &backendv1.JobEvent{JobId: "job-a", BackendSequence: 1, BackendEventId: "evt-1"}
	if err := store.AppendPending(ctx, event); err != nil {
		t.Fatalf("buffering an event: %v", err)
	}
	event.BackendEventId = "mutated"

	pending, _ := store.PendingEvents(ctx)
	if pending[0].GetBackendEventId() != "evt-1" {
		t.Fatalf("buffered event id = %q, want %q", pending[0].GetBackendEventId(), "evt-1")
	}
}

func TestAckRecordsTheAcknowledgedSequenceOnTheJob(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := NewMemoryStore()

	if err := store.SaveJob(ctx, JobRecord{JobID: "job-a", RunID: "run-a", LastSequence: 5}); err != nil {
		t.Fatalf("saving a job: %v", err)
	}
	if err := store.Ack(ctx, "job-a", 4); err != nil {
		t.Fatalf("acknowledging events: %v", err)
	}

	jobs, err := store.Jobs(ctx)
	if err != nil {
		t.Fatalf("reading jobs: %v", err)
	}
	if len(jobs) != 1 || jobs[0].AckedSequence != 4 {
		t.Fatalf("job acked sequence = %+v, want 4", jobs)
	}
}

func TestIdentityRoundTrip(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := NewMemoryStore()

	if _, found, err := store.LoadIdentity(ctx); err != nil || found {
		t.Fatalf("a fresh store has no identity: found=%v err=%v", found, err)
	}

	want := Identity{BackendInstanceID: "backend-1", Token: "t0ken", Name: "laptop"}
	if err := store.SaveIdentity(ctx, want); err != nil {
		t.Fatalf("saving the identity: %v", err)
	}

	got, found, err := store.LoadIdentity(ctx)
	if err != nil || !found {
		t.Fatalf("loading the identity: found=%v err=%v", found, err)
	}
	if got != want {
		t.Fatalf("identity = %+v, want %+v", got, want)
	}
}

func TestRunsAndJobsAreReportedSorted(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := NewMemoryStore()

	for _, runID := range []string{"run-c", "run-a", "run-b"} {
		if err := store.SaveRun(ctx, RunRecord{RunID: runID}); err != nil {
			t.Fatalf("saving a run: %v", err)
		}
	}

	runs, err := store.Runs(ctx)
	if err != nil {
		t.Fatalf("reading runs: %v", err)
	}
	for i, want := range []string{"run-a", "run-b", "run-c"} {
		if runs[i].RunID != want {
			t.Fatalf("runs[%d] = %q, want %q", i, runs[i].RunID, want)
		}
	}
}
