package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/storage/postgres"
	"github.com/rclsilver/threavia/internal/core/storage/postgres/pgtest"
	"github.com/rclsilver/threavia/migrations"
)

func TestJobMetricsAreTransactionalAndKeepExecutionDirectory(t *testing.T) {
	store, db := pgtest.New(t)
	ctx := context.Background()
	f := newFixture(t, store, ctx)
	first := domain.KnownDirectory{ID: domain.NewKnownDirectoryID(), ProjectID: f.project.ID, Name: "first"}
	second := domain.KnownDirectory{ID: domain.NewKnownDirectoryID(), ProjectID: f.project.ID, Name: "second"}
	for _, d := range []*domain.KnownDirectory{&first, &second} {
		if err := store.CreateKnownDirectory(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.SetSessionWorkingDirectory(ctx, f.owner, f.session.ID, &first.ID); err != nil {
		t.Fatal(err)
	}
	transition := func(from, to domain.JobStatus) {
		t.Helper()
		if _, err := store.TransitionJob(ctx, f.job.ID, from, to, nil); err != nil {
			t.Fatal(err)
		}
	}
	// A Job parked before its first execution must still freeze its cwd once
	// it starts, even when its ordinary started_at timestamp is absent.
	transition(domain.JobQueued, domain.JobWaitingBackend)
	transition(domain.JobWaitingBackend, domain.JobRunning)
	if _, err := store.SetSessionWorkingDirectory(ctx, f.owner, f.session.ID, &second.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool().Exec(ctx, `UPDATE jobs SET state_entered_at = clock_timestamp() - interval '10 seconds' WHERE id = $1`, f.job.ID); err != nil {
		t.Fatal(err)
	}

	read := func() postgres.MetricsSnapshot {
		t.Helper()
		s, err := store.ReadMetrics(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	snapshot := read()
	if len(snapshot.Active) != 1 || snapshot.Active[0].KnownDirectoryID != string(first.ID) || snapshot.Active[0].Seconds < 9 {
		t.Fatalf("active job attribution/age = %+v", snapshot.Active)
	}
	rollback := errors.New("rollback")
	err := store.WithTx(ctx, func(tx *postgres.Store) error {
		if _, err := tx.TransitionJob(ctx, f.job.ID, domain.JobRunning, domain.JobCompleted, nil); err != nil {
			return err
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	if got := read(); len(got.Finished) != 0 || got.Active[0].State != "RUNNING" {
		t.Fatalf("rolled back metrics = %+v", got)
	}

	transition(domain.JobRunning, domain.JobWaitingValidation)
	transition(domain.JobWaitingValidation, domain.JobRunning)
	transition(domain.JobRunning, domain.JobCompleted)
	if _, err := store.TransitionJob(ctx, f.job.ID, domain.JobRunning, domain.JobCompleted, nil); !errors.Is(err, postgres.ErrNotFound) {
		t.Fatalf("replay = %v", err)
	}
	snapshot = read()
	if len(snapshot.Active) != 0 || len(snapshot.Finished) != 1 || snapshot.Finished[0].Count != 1 || snapshot.Finished[0].KnownDirectoryID != string(first.ID) {
		t.Fatalf("finished metrics = %+v", snapshot)
	}
	var running postgres.MetricGroup
	for _, g := range snapshot.Durations {
		if g.State == "RUNNING" {
			running = g
		}
	}
	if running.Count != 2 || running.Seconds < 9 || running.KnownDirectoryID != string(first.ID) {
		t.Fatalf("running intervals = %+v", running)
	}
	// A new Store simulates losing all process state. Project deletion must
	// not remove durable counters or reassign the directory.
	if err := store.DeleteProject(ctx, f.owner, f.project.ID); err != nil {
		t.Fatal(err)
	}
	restored, err := postgres.NewStore(db).ReadMetrics(ctx)
	if err != nil || len(restored.Finished) != 1 || restored.Finished[0].Count != 1 {
		t.Fatalf("restored = %+v, %v", restored, err)
	}
}

func TestJobMetricsMigrationStartsAnObservationBaseline(t *testing.T) {
	store, db := pgtest.New(t)
	ctx := context.Background()
	active := newFixture(t, store, ctx)
	finished := newFixture(t, store, ctx)
	if _, err := store.TransitionJob(ctx, active.job.ID, domain.JobQueued, domain.JobRunning, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.TransitionJob(ctx, finished.job.ID, domain.JobQueued, domain.JobCancelled, nil); err != nil {
		t.Fatal(err)
	}
	// Roll back and reapply only this migration in the isolated test schema.
	for _, file := range []string{"000023_job_metrics.down.sql", "000023_job_metrics.up.sql"} {
		body, err := migrations.FS.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Pool().Exec(ctx, string(body)); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
	}
	snapshot, err := store.ReadMetrics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Active) != 1 || snapshot.Active[0].State != "RUNNING" || snapshot.Active[0].Seconds > 5 || snapshot.Active[0].KnownDirectoryID != "" {
		t.Fatalf("baseline = %+v", snapshot)
	}
	if len(snapshot.Finished) != 0 || len(snapshot.Durations) != 0 {
		t.Fatalf("invented historical metrics = %+v", snapshot)
	}
	if _, err := store.TransitionJob(ctx, active.job.ID, domain.JobRunning, domain.JobCompleted, nil); err != nil {
		t.Fatal(err)
	}
	snapshot, err = store.ReadMetrics(ctx)
	if err != nil || len(snapshot.Finished) != 1 || snapshot.Finished[0].Count != 1 {
		t.Fatalf("new observation = %+v, %v", snapshot, err)
	}
}
