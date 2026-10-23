package externalmcp

import (
	"context"
	"encoding/json"
	"fmt"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"time"
)

func (c *Connection) ResourceCall(ctx context.Context, op string, input map[string]any) (any, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var v any
	var err error
	text := func(k string) string { s, _ := input[k].(string); return s }
	switch op {
	case "list_resources":
		v, err = c.Session.ListResources(ctx, &sdk.ListResourcesParams{Cursor: text("cursor")})
	case "read_resource":
		v, err = c.Session.ReadResource(ctx, &sdk.ReadResourceParams{URI: text("uri")})
	case "list_prompts":
		v, err = c.Session.ListPrompts(ctx, &sdk.ListPromptsParams{Cursor: text("cursor")})
	case "get_prompt":
		args := map[string]string{}
		if raw, ok := input["arguments"].(map[string]any); ok {
			for k, value := range raw {
				s, ok := value.(string)
				if !ok {
					return nil, fmt.Errorf("prompt arguments must be strings")
				}
				args[k] = s
			}
		}
		v, err = c.Session.GetPrompt(ctx, &sdk.GetPromptParams{Name: text("name"), Arguments: args})
	default:
		return nil, fmt.Errorf("unknown resource operation")
	}
	if err != nil {
		return nil, fmt.Errorf("MCP resource or prompt request failed")
	}
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: string(data)}}}, nil
}
