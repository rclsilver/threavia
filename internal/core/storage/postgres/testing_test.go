package postgres_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/storage/postgres"
)

// TestPostgresURLEnv names the database the integration tests run against.
// Without it they skip, so `go test ./...` stays green on a machine with no
// database.
const TestPostgresURLEnv = "THREAVIA_TEST_POSTGRES_URL"

// newTestStore gives each test its own PostgreSQL schema, migrated from scratch
// and dropped afterwards, so tests never see each other's rows.
func newTestStore(t *testing.T) (*postgres.Store, context.Context) {
	t.Helper()

	baseURL := os.Getenv(TestPostgresURLEnv)
	if baseURL == "" {
		t.Skipf("set %s to run the database integration tests", TestPostgresURLEnv)
	}

	ctx := context.Background()
	schema := "test_" + strings.ReplaceAll(domain.NewUUID(), "-", "")[:24]

	admin, err := pgx.Connect(ctx, baseURL)
	if err != nil {
		t.Fatalf("connecting to %s: %v", TestPostgresURLEnv, err)
	}
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		_ = admin.Close(ctx)
		t.Fatalf("creating the test schema: %v", err)
	}
	_ = admin.Close(ctx)

	t.Cleanup(func() {
		cleanup, err := pgx.Connect(context.WithoutCancel(ctx), baseURL)
		if err != nil {
			t.Logf("cannot drop the test schema: %v", err)
			return
		}
		defer func() { _ = cleanup.Close(context.WithoutCancel(ctx)) }()
		if _, err := cleanup.Exec(context.WithoutCancel(ctx), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Logf("cannot drop the test schema: %v", err)
		}
	})

	cfg := postgres.Config{URL: scopedURL(baseURL, schema), MaxConns: 4}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := postgres.MigrateUp(cfg, logger); err != nil {
		t.Fatalf("migrating the test schema: %v", err)
	}

	db, err := postgres.Open(ctx, cfg)
	if err != nil {
		t.Fatalf("opening the test pool: %v", err)
	}
	t.Cleanup(db.Close)

	return postgres.NewStore(db), ctx
}

// scopedURL pins every connection to the test schema.
func scopedURL(baseURL, schema string) string {
	separator := "?"
	if strings.Contains(baseURL, "?") {
		separator = "&"
	}
	return fmt.Sprintf("%s%ssearch_path=%s", baseURL, separator, schema)
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

var _ = io.Discard
