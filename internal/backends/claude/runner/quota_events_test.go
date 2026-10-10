package runner

import (
	"encoding/json"
	"testing"
)

func TestClaudeQuotaEventsRetainEveryWindow(t *testing.T) {
	var payload map[string]any
	if err := json.Unmarshal([]byte(`{"status":"allowed_warning","rateLimitType":"five_hour","surpassedThreshold":0.87,"unifiedWindows":{"five_hour":{"utilization":0.91,"resetsAt":1791630000},"seven_day":{"utilization":0},"model_specific":{"status":"restricted"}}}`), &payload); err != nil {
		t.Fatal(err)
	}
	snapshot := claudeRateLimit(payload)
	if len(snapshot.Limits) != 3 {
		t.Fatalf("lost windows: %+v", snapshot)
	}
	for _, limit := range snapshot.Limits {
		switch limit.ID {
		case "five_hour":
			if limit.PercentUsed == nil || *limit.PercentUsed != 91 || limit.ResetsAt == nil || len(limit.Thresholds) != 1 || *limit.Thresholds[0].Value != 0.87 || limit.Status != "allowed_warning" {
				t.Fatalf("lost warning information: %+v", limit)
			}
		case "seven_day":
			if limit.PercentUsed == nil || *limit.PercentUsed != 0 {
				t.Fatal("zero utilization was lost")
			}
		case "model_specific":
			if limit.PercentUsed != nil || limit.Status != "restricted" {
				t.Fatal("missing usage became zero")
			}
		}
	}
	if _, err := snapshot.Struct(); err != nil {
		t.Fatal(err)
	}
}
