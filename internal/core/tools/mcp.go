package tools

func mcpSpecs() []Spec {
	decl := map[string]any{"type": "object", "description": "Portable MCP declaration: name, transport (stdio/http), command/args or url, env/header variable-name references, oauth, tools and readTools exact-name lists, resources/prompts, enabled. No secret values. Changes apply to the next Job."}
	return []Spec{
		{Name: "mcp_server_list", Description: "List MCP server declarations owned by this account and their activation in this project, without secrets.", InputSchema: object(map[string]any{})},
		{Name: "mcp_server_add", RequiresValidation: true, Description: "Add an existing MCP server declaration to Threavia after user validation. Does not install software or enable it in a project. Never put secret values in arguments, URLs or configuration.", InputSchema: object(map[string]any{"server": decl}, "server")},
		{Name: "mcp_server_update", RequiresValidation: true, Description: "Replace an MCP declaration after user validation. List and preserve its existing fields first; changes affect future Jobs only.", InputSchema: object(map[string]any{"serverId": str("Identifier from mcp_server_list."), "server": decl}, "serverId", "server")},
		{Name: "mcp_server_delete", RequiresValidation: true, Description: "Remove an MCP declaration and its project activations after user validation. Does not uninstall software or interrupt existing Jobs. Explain affected projects before requesting deletion.", InputSchema: object(map[string]any{"serverId": str("Identifier from mcp_server_list.")}, "serverId")},
		{Name: "mcp_server_bind", RequiresValidation: true, Description: "Activate/deactivate a registered MCP server in this project for future Jobs. Required servers block startup when unavailable; optional servers produce a warning.", InputSchema: object(map[string]any{"serverId": str("Identifier from mcp_server_list."), "enabled": map[string]any{"type": "boolean"}, "required": map[string]any{"type": "boolean"}}, "serverId", "enabled", "required")},
	}
}
