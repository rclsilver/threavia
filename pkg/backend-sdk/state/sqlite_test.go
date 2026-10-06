package state_test

import (
	"context"
	"path/filepath"
	"testing"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/pkg/backend-sdk/state"
)

func openStore(t *testing.T) (*state.SQLiteStore, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "nested", "backend.db")
	store, err := state.OpenSQLite(path)
	if err != nil {
		t.Fatalf("opening the local state: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, path
}

// TestSQLiteSurvivesARestart pins what makes the state durable: a backend that
// crashes keeps its sequences, its runs and its unacknowledged events.
func TestSQLiteSurvivesARestart(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, path := openStore(t)

	if err := store.SaveIdentity(ctx, state.Identity{
		BackendInstanceID: "backend-1", Token: "credential", Name: "laptop",
	}); err != nil {
		t.Fatalf("saving the identity: %v", err)
	}
	if err := store.SaveRun(ctx, state.RunRecord{
		RunID: "run-1", NativeSessionID: "native-1",
		ResumeStatus: backendv1.ResumeStatus_RESUME_STATUS_AVAILABLE,
	}); err != nil {
		t.Fatalf("saving the run: %v", err)
	}

	for i := 0; i < 3; i++ {
		sequence, err := store.NextSequence(ctx, "job-1")
		if err != nil {
			t.Fatalf("allocating a sequence: %v", err)
		}
		if err := store.AppendPending(ctx, &backendv1.JobEvent{
			BackendEventId: "evt", RunId: "run-1", JobId: "job-1", BackendSequence: sequence,
		}); err != nil {
			t.Fatalf("buffering an event: %v", err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatalf("closing the store: %v", err)
	}

	// The process restarts.
	reopened, err := state.OpenSQLite(path)
	if err != nil {
		t.Fatalf("reopening the local state: %v", err)
	}
	defer func() { _ = reopened.Close() }()

	identity, found, err := reopened.LoadIdentity(ctx)
	if err != nil || !found {
		t.Fatalf("the identity must survive a restart: found=%v err=%v", found, err)
	}
	if identity.BackendInstanceID != "backend-1" || identity.Token != "credential" {
		t.Fatalf("identity = %+v, want the saved one", identity)
	}

	runs, err := reopened.Runs(ctx)
	if err != nil {
		t.Fatalf("reading the runs: %v", err)
	}
	if len(runs) != 1 || runs[0].NativeSessionID != "native-1" ||
		runs[0].ResumeStatus != backendv1.ResumeStatus_RESUME_STATUS_AVAILABLE {
		t.Fatalf("runs = %+v, want the saved one", runs)
	}

	pending, err := reopened.PendingEvents(ctx)
	if err != nil {
		t.Fatalf("reading the buffered events: %v", err)
	}
	if len(pending) != 3 {
		t.Fatalf("%d buffered events survived, want 3", len(pending))
	}
	for i, event := range pending {
		if event.GetBackendSequence() != uint64(i+1) {
			t.Fatalf("buffered events are out of order: %v", pending)
		}
	}

	// The sequence continues where it stopped: Core deduplication and gap
	// detection stay meaningful across a crash.
	next, err := reopened.NextSequence(ctx, "job-1")
	if err != nil {
		t.Fatalf("allocating a sequence: %v", err)
	}
	if next != 4 {
		t.Fatalf("next sequence after a restart = %d, want 4", next)
	}
}

// TestSQLiteAcknowledgement pins that events are dropped only once Core has
// confirmed persisting them.
func TestSQLiteAcknowledgement(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, _ := openStore(t)

	for _, jobID := range []string{"job-a", "job-b"} {
		for i := 0; i < 3; i++ {
			sequence, err := store.NextSequence(ctx, jobID)
			if err != nil {
				t.Fatalf("allocating a sequence: %v", err)
			}
			if err := store.AppendPending(ctx, &backendv1.JobEvent{
				JobId: jobID, BackendSequence: sequence,
			}); err != nil {
				t.Fatalf("buffering an event: %v", err)
			}
		}
	}

	if err := store.Ack(ctx, "job-a", 2); err != nil {
		t.Fatalf("acknowledging events: %v", err)
	}

	pending, err := store.PendingEvents(ctx)
	if err != nil {
		t.Fatalf("reading the buffered events: %v", err)
	}
	if len(pending) != 4 {
		t.Fatalf("%d events still buffered, want 4", len(pending))
	}
	for _, event := range pending {
		if event.GetJobId() == "job-a" && event.GetBackendSequence() <= 2 {
			t.Fatalf("an acknowledged event is still buffered: %v", event)
		}
	}

	// Sequences are per Job: acknowledging one does not touch the other.
	jobs, err := store.Jobs(ctx)
	if err != nil {
		t.Fatalf("reading the jobs: %v", err)
	}
	for _, job := range jobs {
		switch job.JobID {
		case "job-a":
			if job.AckedSequence != 2 {
				t.Errorf("job-a acked sequence = %d, want 2", job.AckedSequence)
			}
		case "job-b":
			if job.AckedSequence != 0 {
				t.Errorf("job-b acked sequence = %d, want 0", job.AckedSequence)
			}
		}
	}
}

// TestSQLiteSatisfiesTheStoreContract keeps the durable implementation
// interchangeable with the in-memory one.
func TestSQLiteSatisfiesTheStoreContract(t *testing.T) {
	t.Parallel()

	store, _ := openStore(t)
	var _ state.Store = store
	var _ state.Store = state.NewMemoryStore()
}
