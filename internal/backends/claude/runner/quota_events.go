package runner

import (
	"sort"
	"strings"
	"time"

	"github.com/rclsilver/threavia/pkg/backend-sdk/quotas"
)

func (c *Claude) InitialQuotas() quotas.Status {
	return quotas.Unavailable("Waiting for quota events from the Claude CLI. Quotas are reported while a job is running.")
}

func claudeRateLimit(info map[string]any) quotas.Status {
	result := quotas.Status{Availability: "AVAILABLE", ObservedAt: time.Now().UTC(), Details: info}
	windows, _ := info["unifiedWindows"].(map[string]any)
	if windows == nil {
		windows = make(map[string]any)
	}
	if kind, _ := info["rateLimitType"].(string); kind != "" {
		merged := make(map[string]any)
		if window, ok := windows[kind].(map[string]any); ok {
			for key, value := range window {
				merged[key] = value
			}
		}
		for key, value := range info {
			if key != "unifiedWindows" {
				merged[key] = value
			}
		}
		windows[kind] = merged
	}
	keys := make([]string, 0, len(windows))
	for key := range windows {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		window, ok := windows[key].(map[string]any)
		if !ok {
			continue
		}
		limit := quotas.Limit{ID: key, Label: strings.ReplaceAll(key, "_", " "), Details: window}
		limit.Status, _ = window["status"].(string)
		if usage, ok := window["utilization"].(float64); ok {
			percentage := usage * 100
			limit.PercentUsed = &percentage
		}
		if reset, ok := window["resetsAt"].(float64); ok {
			at := time.Unix(int64(reset), 0).UTC()
			limit.ResetsAt = &at
		}
		if threshold, ok := window["surpassedThreshold"].(float64); ok {
			limit.Thresholds = []quotas.Threshold{{Label: "Surpassed threshold", Value: &threshold, Unit: "utilization", Status: limit.Status}}
		}
		result.Limits = append(result.Limits, limit)
	}
	return result
}
