package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/storage/postgres"
)

func TestProjectDeletionWaitsForNewWorkAndRefusesIt(t *testing.T) {
	store, ctx := newTestStore(t)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	f := newFixture(t, store, ctx)
	if _, err := store.TransitionJob(ctx, f.job.ID, domain.JobQueued, domain.JobCancelled, nil); err != nil {
		t.Fatal(err)
	}
	inserted, commit := make(chan struct{}), make(chan struct{})
	created, deleted := make(chan error, 1), make(chan error, 1)
	go func() {
		created <- store.WithTx(ctx, func(tx *postgres.Store) error {
			job := domain.Job{ID: domain.NewJobID(), RunID: f.run.ID, Status: domain.JobQueued}
			if err := tx.CreateJob(ctx, &job); err != nil {
				return err
			}
			close(inserted)
			select {
			case <-commit:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	select {
	case <-inserted:
	case err := <-created:
		t.Fatalf("job was not inserted: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	go func() { deleted <- store.DeleteProject(ctx, f.owner, f.project.ID) }()
	select {
	case err := <-deleted:
		close(commit)
		t.Fatalf("deletion bypassed uncommitted work: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(commit)
	if err := <-created; err != nil {
		t.Fatal(err)
	}
	if err := <-deleted; !errors.Is(err, postgres.ErrConflict) {
		t.Fatalf("deletion ignored new work: %v", err)
	}
	if _, err := store.GetSession(ctx, f.owner, f.session.ID); err != nil {
		t.Fatalf("session was lost: %v", err)
	}
}
