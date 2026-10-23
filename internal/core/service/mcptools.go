package service

import (
	"context"
	"encoding/json"
	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/tools"
	"github.com/rclsilver/threavia/pkg/mcpconfig"
)

func (s *Service) mcpTool(ctx context.Context, i auth.Identity, scope jobScope, name tools.Name, input map[string]any) (map[string]any, error) {
	switch name {
	case "mcp_server_list":
		servers, err := s.ListMCP(ctx, i)
		if err != nil {
			return nil, err
		}
		bindings, err := s.MCPBindings(ctx, i, scope.ProjectID)
		if err != nil {
			return nil, err
		}
		raw, _ := json.Marshal(map[string]any{"servers": servers, "bindings": bindings})
		var out map[string]any
		err = json.Unmarshal(raw, &out)
		return out, err
	case "mcp_server_delete":
		err := s.DeleteMCP(ctx, i, text(input, "serverId"))
		return map[string]any{"deleted": err == nil}, err
	case "mcp_server_bind":
		enabled, _ := input["enabled"].(bool)
		required, _ := input["required"].(bool)
		err := s.BindMCP(ctx, i, scope.ProjectID, mcpconfig.Binding{ServerID: text(input, "serverId"), Enabled: enabled, Required: required})
		return map[string]any{"updated": err == nil, "applies": "next Job"}, err
	default:
		raw, err := json.Marshal(input["server"])
		if err != nil {
			return nil, ErrInvalid
		}
		var v mcpconfig.Server
		if json.Unmarshal(raw, &v) != nil {
			return nil, ErrInvalid
		}
		v.ID = text(input, "serverId")
		v, err = s.SaveMCP(ctx, i, v, name == "mcp_server_add")
		return map[string]any{"serverId": v.ID, "applies": "next Job"}, err
	}
}
