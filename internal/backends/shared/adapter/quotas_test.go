package adapter

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/rclsilver/threavia/pkg/backend-sdk/quotas"
)

func TestPartialQuotaUpdatesAndFailedPollKeepOtherLimits(t *testing.T) {
	a := &Adapter{}
	initial := quotas.Status{Availability: "AVAILABLE", ObservedAt: time.Now().UTC(), Limits: []quotas.Limit{{ID: "codex/primary", Label: "Codex"}, {ID: "review/primary", Label: "Review"}}, Details: map[string]any{"families": map[string]any{"codex": map[string]any{"plan": "plus"}, "review": map[string]any{"plan": "custom"}}}}
	if err := a.ReportQuotas(context.Background(), initial, true); err != nil {
		t.Fatal(err)
	}
	updated := quotas.Status{Availability: "AVAILABLE", ObservedAt: initial.ObservedAt.Add(time.Minute), Limits: []quotas.Limit{{ID: "codex/primary", Label: "Updated Codex", Status: "restricted"}}, Details: map[string]any{"families": map[string]any{"codex": map[string]any{"status": "restricted"}}}}
	if err := a.ReportQuotas(context.Background(), updated, false); err != nil {
		t.Fatal(err)
	}
	actual := a.quotaSnapshot
	if len(actual.Limits) != 2 || actual.Limits[0].Status != "restricted" || actual.Limits[1].ID != "review/primary" {
		t.Fatalf("lost a quota family: %+v", actual)
	}
	families := actual.Details["families"].(map[string]any)
	if families["review"] == nil || families["codex"].(map[string]any)["plan"] != "plus" {
		t.Fatal("lost provider metadata")
	}
	if err := a.ReportQuotas(context.Background(), quotas.Unavailable("Refresh failed"), true); err != nil {
		t.Fatal(err)
	}
	actual = a.quotaSnapshot
	if actual.Availability != "UNAVAILABLE" || !actual.ObservedAt.Equal(updated.ObservedAt) || len(actual.Limits) != 2 || actual.Message != "Refresh failed" {
		t.Fatalf("failed poll erased or freshened old data: %+v", actual)
	}
}

func TestOversizedPartialReportKeepsLastValidSnapshot(t *testing.T) {
	a := &Adapter{}
	initial := quotas.Status{Availability: "AVAILABLE", ObservedAt: time.Now().UTC()}
	for i := 0; i < 128; i++ {
		initial.Limits = append(initial.Limits, quotas.Limit{ID: fmt.Sprint(i), Label: fmt.Sprint(i)})
	}
	if err := a.ReportQuotas(context.Background(), initial, true); err != nil {
		t.Fatal(err)
	}
	update := quotas.Status{Availability: "AVAILABLE", ObservedAt: time.Now().UTC(), Limits: []quotas.Limit{{ID: "overflow", Label: "Overflow"}}}
	if err := a.ReportQuotas(context.Background(), update, false); err == nil {
		t.Fatal("accepted an oversized merged report")
	}
	if len(a.quotaSnapshot.Limits) != 128 {
		t.Fatal("invalid report replaced the valid snapshot")
	}
}
