package runner

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"time"

	"github.com/rclsilver/threavia/pkg/backend-sdk/quotas"
)

// Quotas asks app-server for account limits without starting a thread or turn.
func (c *Codex) Quotas(ctx context.Context) quotas.Status {
	if c.options.APIKey != "" {
		return quotas.Status{Availability: "UNSUPPORTED", ObservedAt: time.Now().UTC(), Message: "Account quotas are unavailable for API-key authentication."}
	}
	cmd := exec.CommandContext(ctx, c.binary, "app-server", "--listen", "stdio://")
	cmd.Stderr = io.Discard
	input, err := cmd.StdinPipe()
	if err != nil {
		return quotas.Unavailable("Cannot open the Codex quota reader.")
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		return quotas.Unavailable("Cannot open the Codex quota reader.")
	}
	if err := cmd.Start(); err != nil {
		return quotas.Unavailable("Cannot start the Codex quota reader.")
	}
	defer func() { _ = input.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	r := newRPC(ctx, input, output)
	if err := r.call(ctx, "initialize", map[string]any{"clientInfo": map[string]any{"name": "threavia-quotas", "version": c.options.Version}, "capabilities": map[string]any{"experimentalApi": true}}, nil); err != nil {
		return quotas.Unavailable("Codex quota reader initialization failed.")
	}
	if err := r.send(map[string]any{"method": "initialized", "params": map[string]any{}}); err != nil {
		return quotas.Unavailable("Codex quota reader initialization failed.")
	}
	var result map[string]any
	if err := r.call(ctx, "account/rateLimits/read", map[string]any{}, &result); err != nil {
		return quotas.Unavailable("Codex could not report account quotas; check authentication and CLI support.")
	}
	return codexRateLimits(result)
}

func codexRateLimits(payload map[string]any) quotas.Status {
	result := quotas.Status{Availability: "AVAILABLE", ObservedAt: time.Now().UTC(), Details: payload}
	families := make(map[string]any)
	if all, ok := payload["rateLimitsByLimitId"].(map[string]any); ok {
		for id, value := range all {
			families[id] = value
		}
	}
	if one, ok := payload["rateLimits"].(map[string]any); ok {
		id, _ := one["limitId"].(string)
		if id == "" {
			id = "codex"
		}
		if _, exists := families[id]; !exists {
			families[id] = one
		}
	}
	keys := make([]string, 0, len(families))
	for id := range families {
		keys = append(keys, id)
	}
	sort.Strings(keys)
	for _, id := range keys {
		family, ok := families[id].(map[string]any)
		if !ok {
			continue
		}
		for _, kind := range []string{"primary", "secondary"} {
			window, ok := family[kind].(map[string]any)
			if !ok {
				continue
			}
			limit := quotas.Limit{ID: id + "/" + kind, Label: id + " · " + kind, Details: window}
			limit.Status, _ = window["status"].(string)
			if used, ok := window["usedPercent"].(float64); ok {
				limit.PercentUsed = &used
			}
			if minutes, ok := window["windowDurationMins"].(float64); ok {
				limit.WindowSeconds = int64(minutes * 60)
				limit.Label = fmt.Sprintf("%s · %g min", id, minutes)
			}
			if reset, ok := window["resetsAt"].(float64); ok {
				at := time.Unix(int64(reset), 0).UTC()
				limit.ResetsAt = &at
			}
			result.Limits = append(result.Limits, limit)
		}
	}
	if len(result.Limits) == 0 {
		result.Availability = "UNAVAILABLE"
		result.Message = "Codex did not supply any account quota windows."
	}
	return result
}
