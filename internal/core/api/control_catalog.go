package api

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Explicit allowlist: control tokens cannot create other tokens, change their
// own grants, invoke arbitrary HTTP routes or execute commands on Core.
var controlOperations = []struct{ name, scope, pattern string }{
	{"project_list", "read", "GET /api/v1/projects"},
	{"project_read", "read", "GET /api/v1/projects/{projectId}"},
	{"project_create", "manage", "POST /api/v1/projects"},
	{"project_update", "manage", "PATCH /api/v1/projects/{projectId}"},
	{"project_archive", "manage", "POST /api/v1/projects/{projectId}/archive"},
	{"project_restore", "manage", "POST /api/v1/projects/{projectId}/restore"},
	{"project_delete", "manage", "DELETE /api/v1/projects/{projectId}"},
	{"known_directory_list", "read", "GET /api/v1/directories"},
	{"project_directory_list", "read", "GET /api/v1/projects/{projectId}/directories"},
	{"known_directory_create", "manage", "POST /api/v1/directories"},
	{"project_directory_create", "manage", "POST /api/v1/projects/{projectId}/directories"},
	{"known_directory_update", "manage", "PATCH /api/v1/directories/{directoryId}"},
	{"known_directory_make_global", "manage", "POST /api/v1/directories/{directoryId}/global"},
	{"known_directory_delete", "manage", "DELETE /api/v1/directories/{directoryId}"},
	{"known_directory_bindings", "read", "GET /api/v1/directories/{directoryId}/bindings"},
	{"known_directory_bind", "manage", "POST /api/v1/directories/{directoryId}/bindings"},
	{"known_directory_unbind", "manage", "DELETE /api/v1/directories/{directoryId}/bindings/{backendId}"},
	{"session_list", "read", "GET /api/v1/projects/{projectId}/sessions"},
	{"session_create", "manage", "POST /api/v1/sessions/start"},
	{"session_history", "read", "GET /api/v1/sessions/{sessionId}/events"},
	{"session_update", "manage", "PATCH /api/v1/sessions/{sessionId}"},
	{"session_send", "manage", "POST /api/v1/sessions/{sessionId}/messages"},
	{"session_archive", "manage", "POST /api/v1/sessions/{sessionId}/archive"},
	{"session_restore", "manage", "POST /api/v1/sessions/{sessionId}/restore"},
	{"session_delete", "manage", "DELETE /api/v1/sessions/{sessionId}"},
	{"session_handoff_preview", "read", "GET /api/v1/sessions/{sessionId}/handoff"},
	{"job_cancel", "manage", "POST /api/v1/jobs/{jobId}/cancel"},
	{"job_delete_queued", "manage", "DELETE /api/v1/jobs/{jobId}"},
	{"backend_list", "read", "GET /api/v1/backends"},
	{"backend_read", "read", "GET /api/v1/backends/{backendId}"},
	{"backend_claim", "manage", "POST /api/v1/backends/claim"},
	{"backend_revoke", "manage", "POST /api/v1/backends/{backendId}/revoke"},
	{"backend_registration_token_create", "manage", "POST /api/v1/backend-tokens"},
	{"backend_set_default", "manage", "PUT /api/v1/me/default-backend"},
	{"backend_policy_read", "read", "GET /api/v1/backends/{backendId}/policy"},
	{"backend_policy_set", "manage", "PUT /api/v1/backends/{backendId}/policy"},
	{"project_policy_read", "read", "GET /api/v1/projects/{projectId}/policy"},
	{"project_policy_set", "manage", "PUT /api/v1/projects/{projectId}/policy"},
	{"session_policy_read", "read", "GET /api/v1/sessions/{sessionId}/policy"},
	{"session_policy_set", "manage", "PUT /api/v1/sessions/{sessionId}/policy"},
	{"mcp_server_list", "read", "GET /api/v1/me/mcp"},
	{"mcp_server_add", "manage", "POST /api/v1/me/mcp"},
	{"mcp_server_update", "manage", "PUT /api/v1/mcp/{serverId}"},
	{"mcp_server_delete", "manage", "DELETE /api/v1/mcp/{serverId}"},
	{"mcp_project_list", "read", "GET /api/v1/projects/{projectId}/mcp"},
	{"mcp_project_bind", "manage", "PUT /api/v1/projects/{projectId}/mcp"},
	{"mcp_session_read", "read", "GET /api/v1/sessions/{sessionId}/mcp"},
	{"mcp_session_restrict", "manage", "PUT /api/v1/sessions/{sessionId}/mcp"},
	{"mcp_backend_operation", "manage", "POST /api/v1/backends/{backendId}/mcp"},
	{"audit_read", "read", "GET /api/v1/me/audit"},
}

func (h *handler) controlCatalog() ([]controlDefinition, error) {
	raw, err := h.spec.json()
	if err != nil {
		return nil, err
	}
	var root map[string]any
	if err = json.Unmarshal(raw, &root); err != nil {
		return nil, err
	}
	paths := root["paths"].(map[string]any)
	var out []controlDefinition
	for _, op := range controlOperations {
		method, path, _ := strings.Cut(op.pattern, " ")
		item, ok := paths[path].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("control route absent from contract: %s", op.pattern)
		}
		operation, ok := item[strings.ToLower(method)].(map[string]any)
		if !ok || h.endpoints[op.pattern] == nil {
			return nil, fmt.Errorf("control handler absent: %s", op.pattern)
		}
		properties := map[string]any{}
		required := []any{}
		body := controlObject(controlObject(controlObject(operation["requestBody"])["content"])["application/json"])["schema"]
		if body != nil {
			schema := controlObject(expandControlSchema(root, body, 0))
			for k, v := range controlObject(schema["properties"]) {
				properties[k] = v
			}
			if fields, ok := schema["required"].([]any); ok {
				required = append(required, fields...)
			}
		}
		var parameters []map[string]any
		for _, source := range []any{item["parameters"], operation["parameters"]} {
			params, _ := source.([]any)
			for _, v := range params {
				p := controlObject(expandControlSchema(root, v, 0))
				location, _ := p["in"].(string)
				if location != "path" && location != "query" {
					continue
				}
				name, _ := p["name"].(string)
				properties[name] = p["schema"]
				parameters = append(parameters, p)
				if p["required"] == true {
					required = append(required, name)
				}
			}
		}
		// Channel and native provider identities are never caller-spoofable.
		delete(properties, "channel")
		delete(properties, "nativeSessionId")
		if op.name == "session_create" || op.name == "session_send" {
			properties["idempotencyKey"] = map[string]any{"type": "string", "maxLength": 256}
		}
		description, _ := operation["summary"].(string)
		if detail, ok := operation["description"].(string); ok {
			description += ". " + detail
		}
		if op.name == "session_create" {
			description += ". Starts work immediately with the first message. Use idempotencyKey for retries."
		}
		if op.name == "backend_registration_token_create" {
			description += ". Sensitive one-time registration token: consume through a private provisioning file; never echo it in conversation or logs."
		}
		out = append(out, controlDefinition{name: op.name, pattern: op.pattern, scope: op.scope, description: description, schema: map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}, parameters: parameters})
	}
	empty := map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}
	out = append(out, controlDefinition{name: "instance_info", pattern: "instance_info", scope: "read", description: "Identify this target Core, its version and this connection's live grants before writing.", schema: empty})
	for _, custom := range []struct {
		name, scope, description string
		properties               map[string]any
		required                 []string
	}{
		{"session_read", "read", "Read a session, pending questions and permissions, jobs and recent events. Page backwards with before; the returned cursor is the last included event for session_wait.", map[string]any{"sessionId": controlIDSchema(), "before": map[string]any{"type": "integer", "minimum": 0, "maximum": 9007199254740991}, "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "default": 50}}, []string{"sessionId"}},
		{"session_wait", "read", "Wait at most 30 seconds for forward session events, questions, permissions or work completion. Resume with the returned cursor, never a global snapshot cursor. A timeout is not completion.", map[string]any{"sessionId": controlIDSchema(), "afterSequence": map[string]any{"type": "integer", "minimum": 0, "maximum": 9007199254740991}, "timeoutSeconds": map[string]any{"type": "integer", "minimum": 0, "maximum": 30, "default": 20}}, []string{"sessionId"}},
		{"session_answer", "answer", "Answer a pending question in the specified session. Answers must respect the question's choices. This never approves a permission.", map[string]any{"sessionId": controlIDSchema(), "requestId": controlIDSchema(), "value": map[string]any{"type": "string", "minLength": 1}}, []string{"sessionId", "requestId", "value"}},
		{"session_resolve_validation", "approve", "Accept or refuse a permission using this connection's explicitly delegated approval authority. Copy its exact payloadSha256 from session_read/wait; an agent decision is attributed to this access, not a human click.", map[string]any{"sessionId": controlIDSchema(), "requestId": controlIDSchema(), "payloadSha256": map[string]any{"type": "string", "pattern": "^[a-f0-9]{64}$"}, "approved": map[string]any{"type": "boolean"}, "note": map[string]any{"type": "string", "maxLength": 4000}}, []string{"sessionId", "requestId", "payloadSha256", "approved"}},
	} {
		out = append(out, controlDefinition{name: custom.name, pattern: custom.name, scope: custom.scope, description: custom.description, schema: map[string]any{"type": "object", "properties": custom.properties, "required": custom.required, "additionalProperties": false}})
	}
	return out, nil
}

func controlIDSchema() map[string]any    { return map[string]any{"type": "string", "format": "uuid"} }
func controlObject(v any) map[string]any { m, _ := v.(map[string]any); return m }

// Expand local contract references for portable MCP discovery. No remote schema
// loads, and the depth bound rejects accidentally recursive request contracts.
func expandControlSchema(root map[string]any, v any, depth int) any {
	if depth > 40 {
		return map[string]any{}
	}
	switch value := v.(type) {
	case map[string]any:
		if ref, ok := value["$ref"].(string); ok {
			var target any = root
			for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
				target = controlObject(target)[part]
			}
			return expandControlSchema(root, target, depth+1)
		}
		out := map[string]any{}
		for k, w := range value {
			out[k] = expandControlSchema(root, w, depth+1)
		}
		return out
	case []any:
		out := make([]any, len(value))
		for i, w := range value {
			out[i] = expandControlSchema(root, w, depth+1)
		}
		return out
	default:
		return v
	}
}
