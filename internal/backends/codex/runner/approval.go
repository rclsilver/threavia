package runner

import (
	"context"
	"errors"
	"fmt"
	"github.com/rclsilver/threavia/internal/backends/shared/mcp"
)

func (c *Codex) answer(ctx context.Context, live *execution, jobID, method string, p map[string]any) (any, error) {
	c.logger.Debug("Codex server request", "method", method)
	if c.asker == nil {
		return nil, errors.New("permission gate is not configured")
	}
	decision := func(name string, input map[string]any) (bool, error) {
		d, err := c.decide(ctx, live, jobID, name, input, false)
		return d.Approved, err
	}
	result := func(approved bool, err error) (any, error) {
		value := "decline"
		if approved && err == nil {
			value = "accept"
		}
		return map[string]any{"decision": value}, nil
	}
	switch method {
	case "item/commandExecution/requestApproval":
		live.mu.Lock()
		pcy := live.policy
		live.mu.Unlock()
		// A command approved outside the read-only sandbox can mutate files.
		// Keep a read-only policy read-only even for an unclassified command.
		if !pcy.AllowFilesystemWrite {
			return result(false, nil)
		}
		if network, _ := p["networkApprovalContext"].(map[string]any); network != nil {
			approved, err := decision("WebFetch", map[string]any{"url": str(network, "host"), "approval": p})
			if !approved || err != nil {
				return result(false, err)
			}
		}
		// Broad permission overlays can outlive one operation. Do not grant
		// them as a side effect of approving a single command.
		if extra, _ := p["additionalPermissions"].(map[string]any); len(extra) > 0 {
			return result(false, nil)
		}
		command := str(p, "command")
		if command == "" {
			live.mu.Lock()
			command = str(live.items[str(p, "itemId")], "command")
			live.mu.Unlock()
		}
		if command == "" {
			return result(false, nil)
		}
		// Preserve the complete request, including extra filesystem/network grants,
		// in the validation payload the user reviews.
		input := map[string]any{"command": command, "cwd": p["cwd"], "approval": p}
		live.mu.Lock()
		if original := live.hookInputs[str(p, "itemId")]; original != nil {
			input = cloneInput(original)
			input["approval"] = p
		}
		live.mu.Unlock()
		approved, err := decision("Bash", input)
		return result(approved, err)
	case "item/fileChange/requestApproval":
		live.mu.Lock()
		item := live.items[str(p, "itemId")]
		live.mu.Unlock()
		changes, _ := item["changes"].([]any)
		if len(changes) == 0 {
			return result(false, nil)
		}
		for _, change := range changes {
			file, _ := change.(map[string]any)
			path := str(file, "path")
			if path == "" {
				return result(false, nil)
			}
			approved, err := decision("Edit", map[string]any{"file_path": path, "change": file, "approval": p})
			if !approved || err != nil {
				return result(false, err)
			}
		}
		return result(true, nil)
	case "item/tool/requestUserInput":
		answers := map[string]any{}
		questions, _ := p["questions"].([]any)
		for _, entry := range questions {
			q, _ := entry.(map[string]any)
			options, _ := q["options"].([]any)
			choices := make([]string, 0, len(options))
			for _, entry := range options {
				option, _ := entry.(map[string]any)
				choices = append(choices, str(option, "label"))
			}
			text, err := c.asker.AskUser(ctx, jobID, str(q, "question"), choices, true)
			if err != nil {
				return nil, err
			}
			answers[str(q, "id")] = map[string]any{"answers": []string{text}}
		}
		return map[string]any{"answers": answers}, nil
	case "item/permissions/requestApproval":
		// Session-wide grants would let later actions bypass the policy gate.
		// Declining is safe; Codex can retry as a per-command approval.
		return map[string]any{"permissions": map[string]any{}, "scope": "turn"}, nil
	case "mcpServer/elicitation/request":
		// This server is the per-Job endpoint owned by the backend. It never
		// emits elicitations itself; Codex synthesizes a consent form for MCP
		// calls. The endpoint separately enforces policy and human validation,
		// including questions and web, so consent must not reject every call.
		if str(p, "serverName") == mcp.ServerName && str(p, "mode") == "form" && ctx.Err() == nil {
			return map[string]any{"action": "accept", "content": map[string]any{}}, nil
		}
		return map[string]any{"action": "decline", "content": nil}, nil
	default:
		return nil, fmt.Errorf("unsupported app-server request: %s", method)
	}
}
