package api_test

import (
	"context"
	"testing"
	"time"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/pkg/backend-sdk/quotas"
)

// Each backend reports every minute, and each event makes every client read
// the backend list again: only a change, or an observation grown old, is news.
func TestUnchangedQuotaReportsAreNotNews(t *testing.T) {
	c := newCore(t)
	ctx := context.Background()
	id, _ := c.registerBackend("Quota reporter")
	instanceID := domain.BackendInstanceID(id)

	start := time.Date(2026, 10, 31, 10, 0, 0, 0, time.UTC)
	used := func(value float64) []quotas.Limit {
		reset := start.Add(5 * time.Hour)
		return []quotas.Limit{{ID: "five-hours", Label: "5 hours", PercentUsed: &value, ResetsAt: &reset}}
	}
	report := func(observedAt time.Time, limits []quotas.Limit) {
		t.Helper()
		snapshot, err := (quotas.Status{Availability: "AVAILABLE", ObservedAt: observedAt, Limits: limits,
			Details: map[string]any{"plan": "max", "rate": map[string]any{"b": 2.0, "a": 1.0}}}).Struct()
		if err != nil {
			t.Fatal(err)
		}
		c.svc.QuotaReport(ctx, instanceID, &backendv1.QuotaReport{Snapshot: snapshot})
	}
	events := func() int {
		t.Helper()
		var count int
		if err := c.db.Pool().QueryRow(ctx, `SELECT count(*) FROM events WHERE type = 'backend.quotas_updated' AND payload->>'backendInstanceId' = $1`, id).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	observed := func() time.Time {
		t.Helper()
		backend, err := c.store.BackendInstanceByID(ctx, instanceID)
		if err != nil || backend.Quotas == nil {
			t.Fatal("quotas not stored", err)
		}
		return backend.Quotas.ObservedAt
	}

	report(start, used(10))
	if events() != 1 {
		t.Fatalf("first report: %d events, want 1", events())
	}
	report(start.Add(time.Minute), used(10))
	report(start.Add(2*time.Minute), used(10))
	if events() != 1 || !observed().Equal(start) {
		t.Fatalf("unchanged reports: %d events, observed %s; want 1 event, observed %s", events(), observed(), start)
	}
	report(start.Add(3*time.Minute), used(12))
	if events() != 2 || !observed().Equal(start.Add(3*time.Minute)) {
		t.Fatalf("changed report: %d events, observed %s; want 2", events(), observed())
	}
	report(start.Add(18*time.Minute), used(12))
	if events() != 3 || !observed().Equal(start.Add(18*time.Minute)) {
		t.Fatalf("stale observation: %d events, observed %s; want 3", events(), observed())
	}
}
