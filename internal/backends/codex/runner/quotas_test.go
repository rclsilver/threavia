package runner

import (
	"encoding/json"
	"testing"
)

func TestCodexQuotaFamiliesIncludeAllWindows(t *testing.T) {
	var payload map[string]any
	if err := json.Unmarshal([]byte(`{"rateLimits":{"limitId":"codex","primary":{"usedPercent":99,"windowDurationMins":300,"resetsAt":1791630000}},"rateLimitsByLimitId":{"codex":{"primary":{"usedPercent":0,"windowDurationMins":300},"secondary":{"usedPercent":72,"windowDurationMins":10080}},"review":{"primary":{"status":"unknown"}}},"credits":{"unlimited":true}}`), &payload); err != nil {
		t.Fatal(err)
	}
	snapshot := codexRateLimits(payload)
	if len(snapshot.Limits) != 3 {
		t.Fatalf("lost quota families: %+v", snapshot)
	}
	if snapshot.Limits[0].PercentUsed == nil || *snapshot.Limits[0].PercentUsed != 0 || snapshot.Limits[0].WindowSeconds != 18000 {
		t.Fatal("did not prefer the complete family map or lost zero usage")
	}
	if snapshot.Limits[2].PercentUsed != nil {
		t.Fatal("missing usage became zero")
	}
	if _, err := snapshot.Struct(); err != nil {
		t.Fatal(err)
	}
}
