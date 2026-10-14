package postgres_test

import (
	"context"
	"testing"

	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/storage/postgres/pgtest"
	"github.com/rclsilver/threavia/migrations"
)

func TestGlobalMemoryMigrationPreservesProjectFactsAndRefusesToDiscardGlobalFacts(t *testing.T) {
	store, db := pgtest.New(t)
	ctx := context.Background()
	f := newFixture(t, store, ctx)
	local := domain.Decision{ID: domain.NewDecisionID(), ProjectID: f.project.ID, Title: "Existing fact", Content: "Keep this text", Importance: domain.DecisionImportant, Status: domain.DecisionActive}
	if err := store.CreateDecision(ctx, &local); err != nil {
		t.Fatal(err)
	}
	down, err := migrations.FS.ReadFile("000020_global_memory.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, err := migrations.FS.ReadFile("000020_global_memory.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool().Exec(ctx, string(down)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool().Exec(ctx, string(up)); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetDecision(ctx, f.owner, local.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.OwnerID != f.owner || got.ProjectID != f.project.ID || got.Content != local.Content || got.Importance != local.Importance {
		t.Fatalf("migration changed project memory: %+v", got)
	}

	global := domain.Decision{ID: domain.NewDecisionID(), OwnerID: f.owner, Title: "Shared fact", Importance: domain.DecisionImportant, Status: domain.DecisionActive}
	if err := store.CreateDecision(ctx, &global); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool().Exec(ctx, string(down)); err == nil {
		t.Fatal("rollback silently discarded global memory")
	}
	if got, err := store.GetDecision(ctx, f.owner, global.ID); err != nil || got.Title != global.Title {
		t.Fatalf("refused rollback lost global fact: %+v, %v", got, err)
	}
}
