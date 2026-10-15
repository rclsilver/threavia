package runner

import (
	"context"
	"encoding/json"
	"io"
	"os/exec"
)

func (c *Codex) Authenticated(ctx context.Context) (bool, bool) {
	// Presence of an explicitly configured API key says nothing about its
	// validity. Its next real request is the authority; never probe by inference.
	if c.options.APIKey != "" {
		return false, false
	}
	cmd := exec.CommandContext(ctx, c.binary, "app-server", "--listen", "stdio://")
	cmd.Stderr = io.Discard
	input, err := cmd.StdinPipe()
	if err != nil {
		return false, false
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		_ = input.Close()
		return false, false
	}
	if err := cmd.Start(); err != nil {
		_ = input.Close()
		return false, false
	}
	defer func() { _ = input.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	r := newRPC(ctx, input, output)
	if r.call(ctx, "initialize", map[string]any{"clientInfo": map[string]any{"name": "threavia-auth", "version": c.options.Version}}, nil) != nil {
		return false, false
	}
	if r.send(map[string]any{"method": "initialized", "params": map[string]any{}}) != nil {
		return false, false
	}
	var status struct {
		Account  json.RawMessage `json:"account"`
		Required *bool           `json:"requiresOpenaiAuth"`
	}
	if r.call(ctx, "account/read", map[string]any{"refreshToken": true}, &status) != nil {
		return false, false
	}
	if status.Required == nil {
		return false, false
	}
	return !*status.Required || (len(status.Account) > 0 && string(status.Account) != "null"), true
}
