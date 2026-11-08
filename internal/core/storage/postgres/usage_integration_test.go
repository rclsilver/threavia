package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/events"
	"github.com/rclsilver/threavia/internal/core/storage/postgres"
	"github.com/rclsilver/threavia/internal/core/storage/postgres/pgtest"
	"github.com/rclsilver/threavia/migrations"
	"github.com/rclsilver/threavia/pkg/backend-sdk/quotas"
)

// appendJobEvent appends an event of the fixture Job; backendEventID makes it
// a backend event, which a replay cannot append twice.
func appendJobEvent(t *testing.T, store *postgres.Store, f fixture, kind events.Type, backendEventID string, payload any) error {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	record := &events.Record{Envelope: events.Envelope{
		ID: domain.EventID(domain.NewUUID()), Type: kind, Payload: raw,
		ProjectID: &f.project.ID, SessionID: &f.session.ID, RunID: &f.run.ID, JobID: &f.job.ID,
	}, OwnerID: &f.owner}
	if backendEventID != "" {
		record.Origin = &events.BackendOrigin{BackendInstanceID: f.backend.ID, BackendEventID: backendEventID, BackendSequence: 1}
	}
	return store.AppendEvent(context.Background(), record)
}

func TestUsageMetricsCountEachEndOnceAndOutliveTheSession(t *testing.T) {
	store, _ := pgtest.New(t)
	ctx := context.Background()
	f := newFixture(t, store, ctx)

	completed := map[string]any{"model": "opus", "effort": "high", "usage": map[string]any{
		"inputTokens": 10, "outputTokens": 5, "cacheReadTokens": 100, "cacheWriteTokens": 7, "costUsd": 0.5}}
	if err := appendJobEvent(t, store, f, events.TypeJobCompleted, "end-1", completed); err != nil {
		t.Fatal(err)
	}
	if err := appendJobEvent(t, store, f, events.TypeJobCompleted, "end-1", completed); !errors.Is(err, postgres.ErrDuplicateEvent) {
		t.Fatalf("replay = %v", err)
	}
	// A Core-made cancellation of a Job that never reported its usage.
	if err := appendJobEvent(t, store, f, events.TypeJobCancelled, "", map[string]any{"model": "opus", "effort": "high"}); err != nil {
		t.Fatal(err)
	}
	if err := appendJobEvent(t, store, f, events.TypeJobWaitingBackend, "wait-1", map[string]any{"reason": "QUOTA_EXCEEDED"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.TransitionJob(ctx, f.job.ID, domain.JobQueued, domain.JobWaitingBackend, nil); err != nil {
		t.Fatal(err)
	}
	percent, resets := 42.5, time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	if err := store.UpdateBackendQuotas(ctx, f.backend.ID, quotas.Status{Availability: "AVAILABLE", ObservedAt: time.Now().UTC(),
		Limits: []quotas.Limit{{ID: "weekly", Label: "Weekly", PercentUsed: &percent, WindowSeconds: 604800, ResetsAt: &resets}}}); err != nil {
		t.Fatal(err)
	}

	snapshot, err := store.ReadMetrics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byOutcome := map[string]postgres.UsageGroup{}
	for _, g := range snapshot.Usage {
		if g.SessionID != string(f.session.ID) || g.BackendInstanceID != string(f.backend.ID) || g.Model != "opus" || g.Effort != "high" {
			t.Fatalf("usage labels = %+v", g)
		}
		byOutcome[g.Outcome] = g
	}
	if c := byOutcome["COMPLETED"]; c.Jobs != 1 || c.ReportedJobs != 1 || c.InputTokens != 10 || c.OutputTokens != 5 ||
		c.CacheReadTokens != 100 || c.CacheWriteTokens != 7 || c.CostUSD != 0.5 {
		t.Fatalf("completed usage = %+v", c)
	}
	if c := byOutcome["CANCELLED"]; c.Jobs != 1 || c.ReportedJobs != 0 || c.InputTokens != 0 {
		t.Fatalf("cancelled usage = %+v", c)
	}
	if len(snapshot.Waits) != 1 || snapshot.Waits[0].Reason != "QUOTA_EXCEEDED" || snapshot.Waits[0].Count != 1 {
		t.Fatalf("waits = %+v", snapshot.Waits)
	}
	if len(snapshot.Waiting) != 1 || snapshot.Waiting[0].Reason != "BACKEND_DISCONNECTED" {
		t.Fatalf("waiting = %+v", snapshot.Waiting)
	}
	if len(snapshot.Quotas) != 1 || len(snapshot.Quotas[0].Status.Limits) != 1 || *snapshot.Quotas[0].Status.Limits[0].PercentUsed != percent {
		t.Fatalf("quotas = %+v", snapshot.Quotas)
	}

	day := time.Now().UTC()
	rows, err := store.SessionUsage(ctx, f.owner, "", day.AddDate(0, 0, -1), day.AddDate(0, 0, 1), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].SessionTitle != f.session.Title || rows[0].ProjectName != f.project.Name ||
		rows[0].Jobs != 2 || rows[0].ReportedJobs != 1 || rows[0].InputTokens != 10 || rows[0].CacheReadTokens != 100 {
		t.Fatalf("session usage = %+v", rows)
	}
	if rows, err := store.SessionUsage(ctx, f.owner, "", day.AddDate(0, 0, -9), day.AddDate(0, 0, -2), 10); err != nil || len(rows) != 0 {
		t.Fatalf("usage outside the period = %+v, %v", rows, err)
	}
	if rows, err := store.SessionUsage(ctx, "someone-else", "", day.AddDate(0, 0, -1), day.AddDate(0, 0, 1), 10); err != nil || len(rows) != 0 {
		t.Fatalf("usage of another owner = %+v, %v", rows, err)
	}

	if err := store.DeleteSession(ctx, f.owner, f.session.ID); err != nil {
		t.Fatal(err)
	}
	if rows, err := store.SessionUsage(ctx, f.owner, "", day.AddDate(0, 0, -1), day.AddDate(0, 0, 1), 10); err != nil || len(rows) != 0 {
		t.Fatalf("usage of a deleted session = %+v, %v", rows, err)
	}
	// Counters must not go down when a Session goes.
	snapshot, err = store.ReadMetrics(ctx)
	if err != nil || len(snapshot.Usage) != 2 || len(snapshot.Waits) != 1 {
		t.Fatalf("counters after deletion = %+v, %v", snapshot, err)
	}
}

func TestUsageMetricsMigrationRebuildsTheHistory(t *testing.T) {
	store, db := pgtest.New(t)
	ctx := context.Background()
	f := newFixture(t, store, ctx)
	if _, err := db.Pool().Exec(ctx, `UPDATE jobs SET model = 'sonnet', effort = 'low' WHERE id = $1`, f.job.ID); err != nil {
		t.Fatal(err)
	}
	// An end written before payloads carried the model falls back to the Job's.
	if err := appendJobEvent(t, store, f, events.TypeJobFailed, "end-1", map[string]any{"usage": map[string]any{"inputTokens": 3, "outputTokens": 4}}); err != nil {
		t.Fatal(err)
	}
	if err := appendJobEvent(t, store, f, events.TypeJobWaitingBackend, "wait-1", map[string]any{"reason": "RATE_LIMITED"}); err != nil {
		t.Fatal(err)
	}
	before, err := store.ReadMetrics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"000038_usage_metrics.down.sql", "000038_usage_metrics.up.sql"} {
		body, err := migrations.FS.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Pool().Exec(ctx, string(body)); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
	}
	after, err := store.ReadMetrics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Usage) != 1 || after.Usage[0] != before.Usage[0] || after.Usage[0].Model != "sonnet" ||
		after.Usage[0].Effort != "low" || after.Usage[0].Outcome != "FAILED" || after.Usage[0].OutputTokens != 4 {
		t.Fatalf("rebuilt usage = %+v, before %+v", after.Usage, before.Usage)
	}
	if len(after.Waits) != 1 || after.Waits[0] != before.Waits[0] {
		t.Fatalf("rebuilt waits = %+v, before %+v", after.Waits, before.Waits)
	}
	day := time.Now().UTC()
	rows, err := store.SessionUsage(ctx, f.owner, f.project.ID, day.AddDate(0, 0, -1), day, 10)
	if err != nil || len(rows) != 1 || rows[0].Jobs != 1 || rows[0].InputTokens != 3 {
		t.Fatalf("rebuilt daily usage = %+v, %v", rows, err)
	}
}
