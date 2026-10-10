package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/backends/shared/mcp"
	"github.com/rclsilver/threavia/internal/backends/shared/policy"
)

func (c *Codex) decide(ctx context.Context, live *execution, jobID, tool string, input map[string]any, hook bool) (mcp.Decision, error) {
	live.mu.Lock()
	p, cwd, version, nativeRules := live.policy, live.cwd, live.policyVersion, live.nativeRules
	live.mu.Unlock()
	if hook && !nativeRules && p.Mode != backendv1.ExecutionMode_EXECUTION_MODE_INTERACTIVE {
		// The native untrusted approval policy still asks about commands it
		// cannot establish as read-only. Here we only add the explicit rules.
		p.Mode = backendv1.ExecutionMode_EXECUTION_MODE_AUTONOMOUS
	}
	if tool == "apply_patch" {
		p.Mode = backendv1.ExecutionMode_EXECUTION_MODE_AUTONOMOUS
	}
	d := invocationDecision(p, tool, input, cwd)
	if hook && nativeRules && tool == "Bash" && d.Verdict == policy.Ask && p.Mode == backendv1.ExecutionMode_EXECUTION_MODE_GUARDED {
		explicit := p
		explicit.Mode = backendv1.ExecutionMode_EXECUTION_MODE_AUTONOMOUS
		if invocationDecision(explicit, tool, input, cwd).Verdict == policy.Allow && policy.ReadOnlyInvocation(input) {
			d.Verdict = policy.Allow
		}
	}
	if !hook && d.Verdict != policy.Deny {
		live.mu.Lock()
		key := approvalKey(tool, input, cwd)
		if live.approvals[key] > 0 {
			live.approvals[key]--
			live.mu.Unlock()
			return mcp.Decision{Approved: true}, nil
		}
		live.mu.Unlock()
	}
	switch d.Verdict {
	case policy.Deny:
		return mcp.Decision{Reason: d.Reason}, nil
	case policy.Allow:
		return mcp.Decision{Approved: true}, nil
	}
	if c.asker == nil {
		return mcp.Decision{}, fmt.Errorf("permission gate is not configured")
	}
	if p.Mode == backendv1.ExecutionMode_EXECUTION_MODE_SUPERVISED {
		// An explicit ASK stays human. For actions without a native approval
		// path (web, or commands covered by local allow rules), use an isolated
		// Codex reviewer and fall back to the user if it cannot decide.
		explicit := p
		explicit.Mode = backendv1.ExecutionMode_EXECUTION_MODE_AUTONOMOUS
		if invocationDecision(explicit, tool, input, cwd).Verdict == policy.Allow {
			if review, err := c.review(ctx, live, tool, input); err == nil && review.Verdict != "ask" {
				return mcp.Decision{Approved: review.Verdict == "allow", Reason: review.Reason}, nil
			}
		}
	}
	decision, err := c.asker.AskValidation(ctx, jobID, tool, input)
	if err == nil && decision.Approved {
		live.mu.Lock()
		// Do not retain a human approval across a concurrent policy change.
		if live.policyVersion == version && ctx.Err() == nil && hook {
			if live.approvals == nil {
				live.approvals = make(map[string]int)
			}
			live.approvals[approvalKey(tool, input, cwd)]++
		} else if live.policyVersion != version || ctx.Err() != nil {
			decision = mcp.Decision{Reason: "The execution policy changed while awaiting approval. Retry the action under the current policy."}
		}
		live.mu.Unlock()
	}
	return decision, err
}

func invocationDecision(p policy.Policy, tool string, input map[string]any, cwd string) policy.Decision {
	switch tool {
	case "WebSearch":
		return p.EvaluateInvocationAliases(tool, input, cwd, "mcp__threavia__web_search")
	case "WebFetch":
		return p.EvaluateInvocationAliases(tool, input, cwd, "mcp__threavia__web_fetch")
	default:
		return p.EvaluateInvocation(tool, input, cwd)
	}
}

func approvalKey(tool string, input map[string]any, cwd string) string {
	for _, key := range []string{"workdir", "cwd"} {
		if dir := str(input, key); dir != "" {
			cwd = dir
			break
		}
	}
	value := str(input, "file_path")
	if value != "" {
		if !filepath.IsAbs(value) {
			value = filepath.Join(cwd, value)
		}
		value = canonicalPath(value)
	}
	if tool == "Bash" {
		value = str(input, "command")
	}
	if value == "" {
		b, _ := json.Marshal(input)
		value = string(b)
	}
	return tool + "\x00" + cwd + "\x00" + value
}

func (c *Codex) preTool(ctx context.Context, live *execution, jobID string, event map[string]any) (any, error) {
	c.logger.Debug("Codex policy hook", "event", str(event, "hook_event_name"), "tool", str(event, "tool_name"), "inputType", fmt.Sprintf("%T", event["tool_input"]))
	deny := func(reason string) (any, error) {
		if reason == "" {
			reason = "Threavia refused this action or could not obtain approval."
		}
		c.logger.Debug("Codex policy refusal", "tool", str(event, "tool_name"), "reason", reason)
		c.reportRefusal(live, event, reason)
		return map[string]any{"decision": "block", "reason": reason}, nil
	}
	if str(event, "hook_event_name") == "SessionStart" {
		live.mu.Lock()
		if !live.hookConfirmed {
			live.hookConfirmed = true
			close(live.hookReady)
		}
		live.mu.Unlock()
		return map[string]any{}, nil
	}
	if str(event, "hook_event_name") != "PreToolUse" {
		return deny("Unsupported policy hook event.")
	}
	live.mu.Lock()
	confirmed := live.hookConfirmed
	helper := live.helpers[str(event, "session_id")]
	live.mu.Unlock()
	if helper != nil {
		if helper.webSearch && str(event, "tool_name") == "webrun" {
			input, _ := event["tool_input"].(map[string]any)
			if searchOnly(input) {
				return map[string]any{}, nil
			}
			return deny("This search helper may only issue web search queries, not open pages or execute other web actions.")
		}
		return deny("This isolated helper may not execute local tools.")
	}
	if live.hookReady != nil && !confirmed {
		return deny("Codex has not completed the policy hook handshake.")
	}
	tool := str(event, "tool_name")
	input, _ := event["tool_input"].(map[string]any)
	if input == nil {
		return deny("Missing tool input.")
	}
	input = cloneInput(input)
	if str(input, "cwd") == "" && str(input, "workdir") == "" && str(event, "cwd") != "" {
		input["cwd"] = event["cwd"]
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	live.mu.Lock()
	workCtx := live.turnCtx
	if workCtx == nil {
		workCtx = live.ctx
	}
	live.mu.Unlock()
	stop := context.AfterFunc(workCtx, cancel)
	defer stop()
	if workCtx.Err() != nil {
		return deny("The current turn has ended.")
	}
	// Threavia's permission/question tools are control-plane operations. Do
	// not recursively ask permission to ask permission.
	if strings.HasPrefix(tool, "mcp__threavia__") && event["threavia_direct"] != true {
		return map[string]any{}, nil
	}
	if event["threavia_direct"] == true {
		// Core tools already define their own scope and mandatory validations.
		// Explicit TOOL rules still take priority over their ordinary autonomy.
		if allowed, reason := c.explicitOnly(ctx, live, jobID, tool, input, event["requires_validation"] == true); !allowed {
			return deny(reason)
		}
		return map[string]any{}, nil
	}
	if name, ok := controlTool(tool); ok {
		// Spawning or waiting for a sub-agent, or updating the plan, acts on
		// nothing: what the sub-agent then does reaches this gate itself.
		if allowed, reason := c.explicitOnly(ctx, live, jobID, name, input, false); !allowed {
			return deny(reason)
		}
		return map[string]any{}, nil
	}
	if tool == "apply_patch" {
		// Resolve native TOOL refusals/asks before checking per-file aliases.
		d, err := c.decide(ctx, live, jobID, tool, input, true)
		if err != nil || !d.Approved {
			return deny(d.Reason)
		}
		paths, err := patchPaths(str(input, "command"))
		if err != nil {
			return deny(err.Error())
		}
		for _, path := range paths {
			d, err := c.decide(ctx, live, jobID, "Edit", map[string]any{"file_path": path, "patch": input, "cwd": input["cwd"], "workdir": input["workdir"]}, true)
			if err != nil || !d.Approved {
				return deny(d.Reason)
			}
		}
		return map[string]any{}, nil
	}
	if tool == "view_image" {
		tool = "Read"
		input = map[string]any{"file_path": input["path"], "cwd": input["cwd"], "workdir": input["workdir"]}
	}
	if tool == "Bash" && str(input, "command") == "" {
		// unified exec spells this argument cmd. stdin transport is disabled.
		input = cloneInput(input)
		input["command"] = input["cmd"]
	}
	if tool == "Bash" && str(event, "tool_use_id") != "" {
		// The native approval command includes the provider's shell wrapper.
		// Retain the exact invocation inspected by this hook for that call ID.
		live.mu.Lock()
		if live.hookInputs == nil {
			live.hookInputs = make(map[string]map[string]any)
		}
		live.hookInputs[str(event, "tool_use_id")] = cloneInput(input)
		live.mu.Unlock()
	}
	d, err := c.decide(ctx, live, jobID, tool, input, true)
	if err != nil || !d.Approved {
		return deny(d.Reason)
	}
	return map[string]any{}, nil
}

func searchOnly(input map[string]any) bool {
	queries, ok := input["search_query"].([]any)
	if !ok || len(queries) == 0 {
		return false
	}
	for key := range input {
		if key != "search_query" && key != "response_length" {
			return false
		}
	}
	return true
}

// A blocked PreToolUse can finish without a native tool item. Persist the
// refusal so the user sees the action and its reason even in that case.
func (c *Codex) reportRefusal(live *execution, event map[string]any, reason string) {
	id := str(event, "tool_use_id")
	if live.sink == nil || id == "" || event["threavia_direct"] == true {
		return
	}
	live.mu.Lock()
	if live.helpers[str(event, "session_id")] != nil || live.refusedItems[id] {
		live.mu.Unlock()
		return
	}
	if live.refusedItems == nil {
		live.refusedItems = make(map[string]bool)
	}
	live.refusedItems[id] = true
	started := live.items[id] != nil
	live.mu.Unlock()
	name := str(event, "tool_name")
	switch name {
	case "apply_patch":
		name = "Edit"
	case "view_image":
		name = "Read"
	case "mcp__threavia__web_search":
		name = "WebSearch"
	case "mcp__threavia__web_fetch":
		name = "WebFetch"
	case "mcp__threavia__update_plan":
		name = "TodoWrite"
	default:
		if control, ok := controlTool(name); ok {
			name = control
		}
	}
	input, _ := event["tool_input"].(map[string]any)
	if !started {
		if err := live.sink.ToolStarted(live.reportCtx, live.runID, live.jobID, id, name, input); err != nil {
			c.logger.Warn("cannot report refused Codex tool start", "error", err)
			return
		}
	}
	if err := live.sink.ToolFailed(live.reportCtx, live.runID, live.jobID, id, name, reason); err != nil {
		c.logger.Warn("cannot report refused Codex tool", "error", err)
	}
}

func cloneInput(input map[string]any) map[string]any {
	out := make(map[string]any, len(input)+1)
	for k, v := range input {
		out[k] = v
	}
	return out
}

// Every source and destination is checked before any part of a patch runs.
func patchPaths(patch string) ([]string, error) {
	lines := strings.Split(strings.TrimSpace(patch), "\n")
	if len(lines) < 3 || lines[0] != "*** Begin Patch" || lines[len(lines)-1] != "*** End Patch" {
		return nil, fmt.Errorf("Cannot establish the paths changed by this patch.")
	}
	var paths []string
	for _, line := range lines {
		for _, prefix := range []string{"*** Add File: ", "*** Update File: ", "*** Delete File: ", "*** Move to: "} {
			if strings.HasPrefix(line, prefix) {
				path := strings.TrimSpace(strings.TrimPrefix(line, prefix))
				if path == "" {
					return nil, fmt.Errorf("Empty patch path.")
				}
				paths = append(paths, path)
			}
		}
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("No patch paths supplied.")
	}
	return paths, nil
}

func shellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }

func verifyHooks(ctx context.Context, r *rpc, cwd, command string) (map[string]any, error) {
	var response struct {
		Data []struct {
			Errors []any `json:"errors"`
			Hooks  []struct {
				Command string `json:"command"`
				Event   string `json:"eventName"`
				Enabled bool   `json:"enabled"`
				Handler string `json:"handlerType"`
				Key     string `json:"key"`
				Hash    string `json:"currentHash"`
			} `json:"hooks"`
		} `json:"data"`
	}
	if err := r.call(ctx, "hooks/list", map[string]any{"cwds": []string{cwd}}, &response); err != nil {
		return nil, fmt.Errorf("Codex policy hooks unavailable: %w", err)
	}
	seen := map[string]bool{}
	state := map[string]any{}
	for _, entry := range response.Data {
		if len(entry.Errors) > 0 {
			return nil, fmt.Errorf("Codex could not load policy hooks")
		}
		for _, h := range entry.Hooks {
			if !h.Enabled {
				continue
			}
			if h.Key == "" || h.Hash == "" {
				return nil, fmt.Errorf("Codex omitted policy hook trust metadata")
			}
			if h.Command != command || h.Handler != "command" || (h.Event != "preToolUse" && h.Event != "sessionStart") {
				// Disable local hooks only for this thread. Never alter the user's
				// hooks.json or persisted trust, and never execute an unrelated hook.
				state[h.Key] = map[string]any{"enabled": false}
				continue
			}
			seen[h.Event] = true
			state[h.Key] = map[string]any{"enabled": true, "trusted_hash": h.Hash}
		}
	}
	if !seen["preToolUse"] || !seen["sessionStart"] {
		return nil, fmt.Errorf("Codex did not load both Threavia policy hooks")
	}
	return state, nil
}
