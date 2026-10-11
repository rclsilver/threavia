package runner

import (
	"encoding/json"
	"testing"

	"github.com/rclsilver/threavia/pkg/backend-sdk/quotas"
)

func TestCodexNamedReserveKeepsProviderLabelAndAllWindows(t *testing.T) {
	var payload map[string]any
	if err := json.Unmarshal([]byte(`{"rateLimitsByLimitId":{"codex":{"primary":{"usedPercent":100,"windowDurationMins":300}},"base_model_inference":{"limitName":"gpt-reserve","normalModelSlug":"gpt-5.6-luna","secondary":{"usedPercent":6,"windowDurationMins":10080,"resetsAt":1792309664}}}}`), &payload); err != nil {
		t.Fatal(err)
	}
	snapshot := codexRateLimits(payload)
	if len(snapshot.Limits) != 2 {
		t.Fatalf("lost reserve: %+v", snapshot)
	}
	reserve := snapshot.Limits[0]
	if reserve.ID != "base_model_inference/secondary" || reserve.Label != "gpt-reserve · gpt-5.6-luna" || reserve.WindowSeconds != 604800 || reserve.PercentUsed == nil || *reserve.PercentUsed != 6 || reserve.ResetsAt == nil {
		t.Fatalf("invalid reserve window: %+v", reserve)
	}
	if _, err := snapshot.Struct(); err != nil {
		t.Fatal(err)
	}
}

func TestCodexDiscoversUnknownFamiliesAndWindows(t *testing.T) {
	var payload map[string]any
	if err := json.Unmarshal([]byte(`{"rateLimitsByLimitId":{"future_family":{"limitName":"New provider quota","normalModelSlug":"future-model","rolling":{"usedPercent":0,"windowDurationMins":4320,"resetsAt":1792309664},"another_window":{"usedPercent":null,"windowDurationMins":120},"credits":{"balance":"20"},"spendControl":{"enabled":true}},"unknown_model":{"normalModelSlug":"new-model-v2","primary":{"usedPercent":8,"windowDurationMins":600}},"no_display_name":{"secondary":{"usedPercent":12,"windowDurationMins":300}}}}`), &payload); err != nil {
		t.Fatal(err)
	}
	snapshot := codexRateLimits(payload)
	if len(snapshot.Limits) != 4 || snapshot.Message != "" {
		t.Fatalf("unexpected report: %+v", snapshot)
	}
	byID := make(map[string]quotas.Limit)
	for _, limit := range snapshot.Limits {
		byID[limit.ID] = limit
	}
	rolling := byID["future_family/rolling"]
	if rolling.Label != "New provider quota · future-model" || rolling.WindowSeconds != 259200 || rolling.PercentUsed == nil || *rolling.PercentUsed != 0 || rolling.ResetsAt == nil {
		t.Fatalf("lost unknown window: %+v", rolling)
	}
	if byID["future_family/another_window"].PercentUsed != nil {
		t.Fatal("missing usage became zero")
	}
	if byID["unknown_model/primary"].Label != "new-model-v2" || byID["no_display_name/secondary"].Label != "no_display_name" {
		t.Fatalf("provider name fallbacks were lost: %+v", snapshot)
	}
	if _, err := snapshot.Struct(); err != nil {
		t.Fatal(err)
	}
}

func TestCodexDoesNotAssumeUnreportedSpecialQuotas(t *testing.T) {
	snapshot := codexRateLimits(map[string]any{"rateLimits": map[string]any{"primary": map[string]any{"usedPercent": float64(10)}}})
	if snapshot.Availability != "AVAILABLE" || snapshot.Message != "" || len(snapshot.Limits) != 1 {
		t.Fatalf("assumed an absent quota: %+v", snapshot)
	}
}
