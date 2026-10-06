package postgres_test

import (
	"context"
	"testing"

	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/storage/postgres"
	"github.com/rclsilver/threavia/internal/core/storage/postgres/pgtest"
)

// newTestStore gives each test its own migrated schema.
func newTestStore(t *testing.T) (*postgres.Store, context.Context) {
	t.Helper()
	store, _ := pgtest.New(t)
	return store, context.Background()
}

// fixture builds a Project, a BackendInstance, a Session, a Run and a Job, which
// is the shape every behaviour in this package operates on.
type fixture struct {
	owner   domain.UserID
	project domain.Project
	backend domain.BackendInstance
	session domain.Session
	run     domain.Run
	job     domain.Job
}

func newFixture(t *testing.T, store *postgres.Store, ctx context.Context) fixture {
	t.Helper()

	owner := domain.UserID("user-" + domain.NewUUID())
	f := fixture{owner: owner}

	f.project = domain.Project{
		ID: domain.NewProjectID(), OwnerID: owner, Name: "homelab",
		Description: "Homelab", Status: domain.ProjectActive,
	}
	if err := store.CreateProject(ctx, &f.project); err != nil {
		t.Fatalf("creating the project: %v", err)
	}

	f.backend = domain.BackendInstance{
		ID: domain.NewBackendInstanceID(), OwnerID: &owner, Name: "laptop",
		OwnershipStatus: domain.BackendClaimed,
		Capabilities:    []domain.Capability{domain.CapabilityCode},
		Capacity:        domain.Capacity{MaxConcurrentRuns: 1},
	}
	if err := store.CreateBackendInstance(ctx, &f.backend, "hash-"+domain.NewUUID(), nil, nil); err != nil {
		t.Fatalf("creating the backend instance: %v", err)
	}

	f.session = domain.Session{
		ID: domain.NewSessionID(), ProjectID: f.project.ID,
		Title: "Analyse ce projet", Status: domain.SessionActive,
	}
	if err := store.CreateSession(ctx, &f.session); err != nil {
		t.Fatalf("creating the session: %v", err)
	}

	f.run = domain.Run{
		ID: domain.NewRunID(), SessionID: f.session.ID,
		BackendInstanceID: f.backend.ID, ResumeStatus: domain.ResumeUnknown,
	}
	if err := store.CreateRun(ctx, &f.run); err != nil {
		t.Fatalf("creating the run: %v", err)
	}

	f.job = domain.Job{ID: domain.NewJobID(), RunID: f.run.ID, Status: domain.JobQueued}
	if err := store.CreateJob(ctx, &f.job); err != nil {
		t.Fatalf("creating the job: %v", err)
	}

	return f
}
