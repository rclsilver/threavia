package runner

import (
	"encoding/json"
	"testing"
)

func TestClaudeUsageRetainsAllScopedLimits(t *testing.T) {
	var payload map[string]any
	if err := json.Unmarshal([]byte(`{"rate_limits_available":true,"rate_limits":{"limits":[{"kind":"session","group":"session","percent":0,"severity":"normal","scope":null},{"kind":"weekly_all","group":"weekly","percent":3,"resets_at":"2026-10-17T11:00:00.322570Z"},{"kind":"weekly_scoped","group":"weekly","percent":2,"scope":{"model":{"id":null,"display_name":"Fable"}},"resets_at":"2026-10-17T11:00:00Z"},{"kind":"weekly_scoped","group":"weekly","scope":{"model":{"id":"other","display_name":"Other model"}}}]}}`), &payload); err != nil {
		t.Fatal(err)
	}
	snapshot := claudeUsage(payload)
	if len(snapshot.Limits) != 4 {
		t.Fatalf("lost scoped limits: %+v", snapshot)
	}
	if snapshot.Limits[0].PercentUsed == nil || *snapshot.Limits[0].PercentUsed != 0 || snapshot.Limits[0].WindowSeconds != 18000 {
		t.Fatal("lost zero usage or the five-hour window")
	}
	fable := snapshot.Limits[2]
	if fable.Label != "Fable" || fable.WindowSeconds != 604800 || fable.PercentUsed == nil || *fable.PercentUsed != 2 || fable.ResetsAt == nil {
		t.Fatalf("lost Fable: %+v", fable)
	}
	if snapshot.Limits[3].PercentUsed != nil {
		t.Fatal("missing usage became zero")
	}
	if _, err := snapshot.Struct(); err != nil {
		t.Fatal(err)
	}
}

func TestClaudeUsageOlderModelScopedResponse(t *testing.T) {
	var payload map[string]any
	_ = json.Unmarshal([]byte(`{"rate_limits":{"five_hour":{"utilization":0},"seven_day":{"utilization":10},"model_scoped":[{"display_name":"Fable","utilization":2,"resets_at":"2026-10-17T11:00:00Z"}]}}`), &payload)
	snapshot := claudeUsage(payload)
	if len(snapshot.Limits) != 3 || snapshot.Limits[1].Label != "Fable" || *snapshot.Limits[1].PercentUsed != 2 {
		t.Fatalf("lost older scoped usage: %+v", snapshot)
	}
	if _, err := snapshot.Struct(); err != nil {
		t.Fatal(err)
	}
}

func TestClaudeUsageAvailability(t *testing.T) {
	if snapshot := claudeUsage(map[string]any{"rate_limits_available": false}); snapshot.Availability != "UNSUPPORTED" {
		t.Fatalf("unexpected state: %+v", snapshot)
	}
	if snapshot := claudeUsage(map[string]any{}); snapshot.Availability != "UNAVAILABLE" {
		t.Fatalf("empty report became available: %+v", snapshot)
	}
}

func TestClaudeUsageUnknownKindAndScopeDimensions(t *testing.T) {
	var payload map[string]any
	if err := json.Unmarshal([]byte(`{"rate_limits":{"limits":[{"kind":"future_scoped","group":"new_period","window_seconds":259200,"percent":0,"scope":{"model":{"display_name":"Future model"},"surface":{"id":"terminal","display_name":"Terminal"},"region":"Europe"},"resets_at":"2026-10-17T11:00:00Z"},{"kind":"future_scoped","window_duration_mins":4320,"scope":{"model":{"display_name":"Future model"},"surface":{"id":"web","display_name":"Web"},"region":"Europe"}},{"kind":"session","window_seconds":90000,"percent":9}],"model_scoped":[{"display_name":"Legacy mirror","utilization":50}]}}`), &payload); err != nil {
		t.Fatal(err)
	}
	snapshot := claudeUsage(payload)
	if len(snapshot.Limits) != 3 {
		t.Fatalf("lost or duplicated windows: %+v", snapshot)
	}
	first, second := snapshot.Limits[0], snapshot.Limits[1]
	if first.Label != "Future model · Europe · Terminal" || second.Label != "Future model · Europe · Web" || first.ID == second.ID {
		t.Fatalf("lost scope dimensions: %+v", snapshot)
	}
	if first.WindowSeconds != 259200 || second.WindowSeconds != 259200 || first.PercentUsed == nil || *first.PercentUsed != 0 || first.ResetsAt == nil || second.PercentUsed != nil {
		t.Fatalf("lost future quota values: %+v", snapshot)
	}
	if snapshot.Limits[2].WindowSeconds != 90000 || snapshot.Limits[2].Label == "5h" {
		t.Fatal("ignored explicit provider duration")
	}
	limits := payload["rate_limits"].(map[string]any)["limits"].([]any)
	limits[0].(map[string]any)["percent"] = float64(40)
	limits[0].(map[string]any)["resets_at"] = "2026-10-18T11:00:00Z"
	if next := claudeUsage(payload); next.Limits[0].ID != first.ID {
		t.Fatal("quota identity changed with its usage")
	}
	if _, err := snapshot.Struct(); err != nil {
		t.Fatal(err)
	}
}

func TestClaudeUsageDiscoversUnknownLegacyWindowsAndArrays(t *testing.T) {
	var payload map[string]any
	if err := json.Unmarshal([]byte(`{"rate_limits":{"30_day":{"utilization":12,"resets_at":"2026-11-01T10:00:00Z"},"project_scoped":[{"scope":{"project":{"display_name":"New project"}},"window_seconds":86400,"utilization":0},{"scope":{"project":{"display_name":"Another project"}},"window_seconds":86400,"utilization":null}],"extra_usage":{"is_enabled":false,"utilization":null},"weekly_scoped_shares":[{"used_percent_of_weekly":2}],"metadata":{"plan":"max"}}}`), &payload); err != nil {
		t.Fatal(err)
	}
	snapshot := claudeUsage(payload)
	if len(snapshot.Limits) != 3 {
		t.Fatalf("lost windows or included metadata: %+v", snapshot)
	}
	if snapshot.Limits[0].WindowSeconds != 2592000 || snapshot.Limits[0].ResetsAt == nil {
		t.Fatal("lost new keyed period")
	}
	if snapshot.Limits[1].Label != "New project" || snapshot.Limits[1].PercentUsed == nil || *snapshot.Limits[1].PercentUsed != 0 || snapshot.Limits[2].PercentUsed != nil {
		t.Fatal("lost new scoped array")
	}
	arrays := payload["rate_limits"].(map[string]any)["project_scoped"].([]any)
	arrays[0].(map[string]any)["utilization"] = float64(30)
	if next := claudeUsage(payload); next.Limits[1].ID != snapshot.Limits[1].ID {
		t.Fatal("legacy quota identity changed with its usage")
	}
	if _, err := snapshot.Struct(); err != nil {
		t.Fatal(err)
	}
}

func TestClaudeEventDiscoversUnknownWindowDurationAndLabel(t *testing.T) {
	window := map[string]any{"utilization": float64(0.25), "display_name": "New event quota"}
	snapshot := claudeRateLimit(map[string]any{"unifiedWindows": map[string]any{"30_day_custom": window}})
	if len(snapshot.Limits) != 1 || snapshot.Limits[0].WindowSeconds != 2592000 || snapshot.Limits[0].Label != "New event quota" || *snapshot.Limits[0].PercentUsed != 25 {
		t.Fatalf("lost future event window: %+v", snapshot)
	}
}
