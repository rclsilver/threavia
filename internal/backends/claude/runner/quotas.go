package runner

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rclsilver/threavia/pkg/backend-sdk/quotas"
)

// Quotas uses the CLI's read-only usage control, without a model turn,
// provider credentials in Threavia, or an independent OAuth request.
func (c *Claude) Quotas(ctx context.Context) quotas.Status {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.binary, "--print", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose", "--no-session-persistence", "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`)
	cmd.Stderr = io.Discard
	cmd.WaitDelay = time.Second
	input, err := cmd.StdinPipe()
	if err != nil {
		return quotas.Unavailable("Cannot open the Claude quota reader.")
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		_ = input.Close()
		return quotas.Unavailable("Cannot open the Claude quota reader.")
	}
	if err := cmd.Start(); err != nil {
		_ = input.Close()
		return quotas.Unavailable("Cannot start the Claude quota reader.")
	}
	defer func() { _ = input.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	send := func(id, subtype string) error {
		return json.NewEncoder(input).Encode(map[string]any{"type": "control_request", "request_id": id, "request": map[string]any{"subtype": subtype, "skip_behaviors": true}})
	}
	if err := send("quotas-init", "initialize"); err != nil {
		return quotas.Unavailable("Claude quota reader initialization failed.")
	}
	scanner := bufio.NewScanner(output)
	scanner.Buffer(make([]byte, 64<<10), maxLine)
	for scanner.Scan() {
		var line struct {
			Type     string `json:"type"`
			Response struct {
				Subtype   string         `json:"subtype"`
				RequestID string         `json:"request_id"`
				Response  map[string]any `json:"response"`
			} `json:"response"`
		}
		if json.Unmarshal(scanner.Bytes(), &line) != nil || line.Type != "control_response" {
			continue
		}
		if line.Response.RequestID == "quotas-init" {
			if line.Response.Subtype != "success" || send("quotas-usage", "get_usage") != nil {
				return quotas.Unavailable("Claude quota reader initialization failed.")
			}
		}
		if line.Response.RequestID == "quotas-usage" {
			if line.Response.Subtype != "success" {
				return quotas.Unavailable("This Claude CLI cannot report account usage.")
			}
			return claudeUsage(line.Response.Response)
		}
	}
	return quotas.Unavailable("Claude could not report account quotas; check authentication and CLI support.")
}

func claudeUsage(payload map[string]any) quotas.Status {
	if available, ok := payload["rate_limits_available"].(bool); ok && !available {
		return quotas.Status{Availability: "UNSUPPORTED", ObservedAt: time.Now().UTC(), Message: "This Claude account does not report subscription quotas."}
	}
	rates, _ := payload["rate_limits"].(map[string]any)
	result := quotas.Status{Availability: "AVAILABLE", ObservedAt: time.Now().UTC(), Details: rates}

	if limits, ok := rates["limits"].([]any); ok {
		// The canonical list is authoritative; do not duplicate legacy mirrors.
		for _, value := range limits {
			window, ok := value.(map[string]any)
			if !ok {
				continue
			}
			kind, _ := window["kind"].(string)
			if kind == "" {
				continue
			}
			limit := quotas.Limit{ID: kind, Label: claudeUsageLabel(kind, window), Details: window,
				WindowSeconds: claudeUsageDuration(kind, window)}
			// Keep existing ids compatible with partial rate-limit notifications.
			if kind == "session" {
				limit.ID = "five_hour"
			}
			if kind == "weekly_all" {
				limit.ID = "seven_day"
			}
			if scope, ok := window["scope"].(map[string]any); ok {
				// All scope dimensions contribute to identity, not just the model.
				data, _ := json.Marshal(scope)
				limit.ID = fmt.Sprintf("%s/%x", kind, sha256.Sum256(data))
			}
			limit.Status, _ = window["severity"].(string)
			limit.PercentUsed, _ = floatPointer(window["percent"])
			limit.ResetsAt = claudeUsageReset(window["resets_at"])
			result.Limits = append(result.Limits, limit)
		}
	} else {
		// Older usage responses expose keyed windows and arrays of scoped windows.
		// Detect their usage fields rather than maintaining a list of quota names.
		keys := make([]string, 0, len(rates))
		for key := range rates {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			switch value := rates[key].(type) {
			case map[string]any:
				if claudeIsUsageWindow(value) {
					limit := claudeUsageWindow(key, claudeUsageLabel(key, value), value)
					limit.WindowSeconds = claudeUsageDuration(key, value)
					result.Limits = append(result.Limits, limit)
				}
			case []any:
				for _, item := range value {
					window, ok := item.(map[string]any)
					if !ok || !claudeIsUsageWindow(window) {
						continue
					}
					// Exclude changing usage fields from the scope identity.
					identity := make(map[string]any)
					for field, value := range window {
						switch field {
						case "utilization", "resets_at", "used_dollars", "limit_dollars", "remaining_dollars", "severity", "status":
							continue
						}
						identity[field] = value
					}
					data, _ := json.Marshal(identity)
					id := fmt.Sprintf("%s/%x", key, sha256.Sum256(data))
					limit := claudeUsageWindow(id, claudeUsageLabel(key, window), window)
					limit.WindowSeconds = claudeUsageDuration(key, window)
					result.Limits = append(result.Limits, limit)
				}
			}
		}
	}
	if len(result.Limits) == 0 {
		result.Availability, result.Message = "UNAVAILABLE", "Claude did not supply any account quota windows."
	}
	return result
}

func claudeIsUsageWindow(window map[string]any) bool {
	if enabled, ok := window["is_enabled"].(bool); ok && !enabled {
		return false
	}
	for _, field := range []string{"utilization", "resets_at", "used_dollars", "limit_dollars", "remaining_dollars"} {
		if _, ok := window[field]; ok {
			return true
		}
	}
	return false
}

func claudeUsageLabel(kind string, window map[string]any) string {
	for _, field := range []string{"display_name", "label", "name"} {
		if label, ok := window[field].(string); ok && label != "" {
			return label
		}
	}
	if label := claudeScopeLabel(window["scope"]); label != "" {
		return label
	}
	// These are protocol periods, never model names or a whitelist of limits.
	if kind == "session" && claudeUsageDuration(kind, window) == 18000 {
		return "5h"
	}
	if kind == "weekly_all" && claudeUsageDuration(kind, window) == 604800 {
		return "7d"
	}
	return strings.ReplaceAll(kind, "_", " ")
}

// Every scope dimension is displayed using its provider name or identifier.
func claudeScopeLabel(value any) string {
	if label, ok := value.(string); ok {
		return label
	}
	scope, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	for _, field := range []string{"display_name", "label", "name", "id"} {
		if label, ok := scope[field].(string); ok && label != "" {
			return label
		}
	}
	keys := make([]string, 0, len(scope))
	for key := range scope {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	labels := make([]string, 0, len(keys))
	for _, key := range keys {
		if label := claudeScopeLabel(scope[key]); label != "" {
			labels = append(labels, label)
		}
	}
	return strings.Join(labels, " · ")
}

func claudeUsageDuration(kind string, window map[string]any) int64 {
	// Explicit provider durations take precedence over known legacy periods.
	for _, field := range []string{"window_seconds", "window_duration_seconds", "windowSeconds"} {
		if seconds, ok := window[field].(float64); ok && seconds > 0 {
			return int64(seconds)
		}
	}
	for _, field := range []string{"window_duration_mins", "windowDurationMins"} {
		if minutes, ok := window[field].(float64); ok && minutes > 0 {
			return int64(minutes * 60)
		}
	}
	group, _ := window["group"].(string)
	if group == "weekly" || kind == "weekly_all" || kind == "model_scoped" {
		return 604800
	}
	if group == "session" || kind == "session" {
		return 18000
	}
	parts := strings.Split(kind, "_")
	if len(parts) < 2 {
		return 0
	}
	count, _ := strconv.ParseInt(parts[0], 10, 64)
	// Existing CLI keys spell these two periods out.
	if parts[0] == "five" {
		count = 5
	}
	if parts[0] == "seven" {
		count = 7
	}
	if count <= 0 {
		return 0
	}
	units := map[string]int64{"second": 1, "minute": 60, "hour": 3600, "day": 86400, "week": 604800}
	return count * units[strings.TrimSuffix(parts[1], "s")]
}

func claudeUsageWindow(id, label string, window map[string]any) quotas.Limit {
	limit := quotas.Limit{ID: id, Label: label, Details: window, ResetsAt: claudeUsageReset(window["resets_at"])}
	if used, ok := window["utilization"].(float64); ok {
		limit.PercentUsed = &used
	}
	limit.Used, _ = floatPointer(window["used_dollars"])
	limit.Limit, _ = floatPointer(window["limit_dollars"])
	limit.Remaining, _ = floatPointer(window["remaining_dollars"])
	if limit.Used != nil || limit.Limit != nil || limit.Remaining != nil {
		limit.Unit = "USD"
	}
	return limit
}

func floatPointer(value any) (*float64, bool) {
	number, ok := value.(float64)
	if !ok {
		return nil, false
	}
	return &number, true
}

func claudeUsageReset(value any) *time.Time {
	text, ok := value.(string)
	if !ok {
		return nil
	}
	at, err := time.Parse(time.RFC3339Nano, text)
	if err != nil {
		return nil
	}
	at = at.UTC()
	return &at
}
