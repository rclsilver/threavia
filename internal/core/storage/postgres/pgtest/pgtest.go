// Package pgtest gives tests a throwaway PostgreSQL schema, migrated from
// scratch and dropped afterwards.
//
// Tests that need a database skip when THREAVIA_TEST_POSTGRES_URL is unset, so
// the suite stays green on a machine without one.
package pgtest

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

// URLEnv names the database the integration tests run against.
const URLEnv = "THREAVIA_TEST_POSTGRES_URL"

// New returns a Store bound to a fresh schema. Each test gets its own, so tests
// never see each other's rows and can run in parallel.
func New(t *testing.T) (*postgres.Store, *postgres.DB) {
	t.Helper()

	baseURL := os.Getenv(URLEnv)
	if baseURL == "" {
		t.Skipf("set %s to run the database integration tests", URLEnv)
	}

	ctx := context.Background()
	schema := "test_" + strings.ReplaceAll(domain.NewUUID(), "-", "")[:24]

	admin, err := pgx.Connect(ctx, baseURL)
	if err != nil {
		t.Fatalf("connecting to %s: %v", URLEnv, err)
	}
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		_ = admin.Close(ctx)
		t.Fatalf("creating the test schema: %v", err)
	}
	_ = admin.Close(ctx)

	t.Cleanup(func() {
		cleanupCtx := context.Background()
		conn, err := pgx.Connect(cleanupCtx, baseURL)
		if err != nil {
			t.Logf("cannot drop the test schema: %v", err)
			return
		}
		defer func() { _ = conn.Close(cleanupCtx) }()
		if _, err := conn.Exec(cleanupCtx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Logf("cannot drop the test schema: %v", err)
		}
	})

	cfg := postgres.Config{URL: scopedURL(baseURL, schema), MaxConns: 8}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := postgres.MigrateUp(cfg, logger); err != nil {
		t.Fatalf("migrating the test schema: %v", err)
	}

	db, err := postgres.Open(ctx, cfg)
	if err != nil {
		t.Fatalf("opening the test pool: %v", err)
	}
	t.Cleanup(db.Close)

	return postgres.NewStore(db), db
}

// scopedURL pins every connection to the test schema.
func scopedURL(baseURL, schema string) string {
	separator := "?"
	if strings.Contains(baseURL, "?") {
		separator = "&"
	}
	return fmt.Sprintf("%s%ssearch_path=%s", baseURL, separator, schema)
}
