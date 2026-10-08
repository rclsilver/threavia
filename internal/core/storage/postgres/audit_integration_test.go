package postgres_test

import (
	"testing"

	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/storage/postgres"
)

// TestAuditListsOneProject pins the filter the Audit page of a Project reads
// through: only what was done in that Project, and everything without one.
func TestAuditListsOneProject(t *testing.T) {
	store, ctx := newTestStore(t)
	f := newFixture(t, store, ctx)

	other := domain.NewProjectID()
	for _, project := range []domain.ProjectID{f.project.ID, f.project.ID, other} {
		if err := store.RecordAudit(ctx, postgres.AuditEntry{
			OwnerID: f.owner, ActorID: string(f.owner),
			Action: "execution_policy.set", ProjectID: string(project),
		}); err != nil {
			t.Fatalf("recording an entry: %v", err)
		}
	}

	mine, err := store.ListAudit(ctx, f.owner, f.project.ID, 10)
	if err != nil {
		t.Fatalf("listing one project: %v", err)
	}
	if len(mine) != 2 {
		t.Fatalf("%d entries for the project, want 2", len(mine))
	}
	all, err := store.ListAudit(ctx, f.owner, "", 10)
	if err != nil {
		t.Fatalf("listing every project: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("%d entries in all, want 3", len(all))
	}
}
