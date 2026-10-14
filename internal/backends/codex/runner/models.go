package runner

import (
	"context"
	"fmt"
	"io"
	"os/exec"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
)

// Models reads the authenticated provider catalog without starting agent work.
func (c *Codex) Models(ctx context.Context) (*backendv1.ModelCatalog, error) {
	cmd := exec.CommandContext(ctx, c.binary, "app-server", "--listen", "stdio://")
	cmd.Stderr = io.Discard
	input, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		return nil, err
	}
	defer func() { _ = input.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	rpc := newRPC(ctx, input, output)
	if err = rpc.call(ctx, "initialize", map[string]any{"clientInfo": map[string]any{"name": "threavia-models", "version": c.options.Version}, "capabilities": map[string]any{"experimentalApi": true}}, nil); err != nil {
		return nil, err
	}
	if err = rpc.send(map[string]any{"method": "initialized", "params": map[string]any{}}); err != nil {
		return nil, err
	}
	if c.options.APIKey != "" {
		if err = rpc.call(ctx, "account/login/start", map[string]any{"type": "apiKey", "apiKey": c.options.APIKey}, nil); err != nil {
			return nil, fmt.Errorf("Codex model catalog authentication failed")
		}
	}
	catalog := &backendv1.ModelCatalog{DefaultModel: c.options.Model, DefaultEffort: c.options.ReasoningEffort}
	var config struct {
		Config struct {
			Model  string `json:"model"`
			Effort string `json:"model_reasoning_effort"`
		} `json:"config"`
	}
	if err = rpc.call(ctx, "config/read", map[string]any{"includeLayers": false}, &config); err != nil {
		return nil, err
	}
	if catalog.DefaultModel == "" {
		catalog.DefaultModel = config.Config.Model
	}
	if catalog.DefaultEffort == "" {
		catalog.DefaultEffort = config.Config.Effort
	}
	cursor := ""
	for page := 0; page < 100; page++ {
		var result struct {
			Data []struct {
				Model     string `json:"model"`
				Name      string `json:"displayName"`
				IsDefault bool   `json:"isDefault"`
				Efforts   []struct {
					Effort string `json:"reasoningEffort"`
				} `json:"supportedReasoningEfforts"`
				DefaultEffort string `json:"defaultReasoningEffort"`
			} `json:"data"`
			NextCursor *string `json:"nextCursor"`
		}
		args := map[string]any{"limit": 100}
		if cursor != "" {
			args["cursor"] = cursor
		}
		if err = rpc.call(ctx, "model/list", args, &result); err != nil {
			return nil, err
		}
		for _, model := range result.Data {
			option := &backendv1.ModelOption{Id: model.Model, Name: model.Name, DefaultEffort: model.DefaultEffort}
			if option.Name == "" {
				option.Name = option.Id
			}
			for _, effort := range model.Efforts {
				option.Efforts = append(option.Efforts, effort.Effort)
			}
			catalog.Models = append(catalog.Models, option)
			if catalog.DefaultModel == "" && model.IsDefault {
				catalog.DefaultModel = model.Model
			}
		}
		if result.NextCursor == nil || *result.NextCursor == "" {
			break
		}
		if *result.NextCursor == cursor {
			return nil, fmt.Errorf("model catalog repeated its cursor")
		}
		cursor = *result.NextCursor
	}
	if len(catalog.Models) == 0 {
		return nil, fmt.Errorf("provider reported no models")
	}
	return catalog, nil
}
