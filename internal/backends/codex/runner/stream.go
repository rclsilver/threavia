package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	contract "github.com/rclsilver/threavia/internal/backends/shared/runner"
)

func (c *Codex) drive(ctx, reportCtx context.Context, r *rpc, live *execution, p contract.StartParams, sink contract.Sink, url string, requests *sync.WaitGroup) outcome {
	out := outcome{code: "PROVIDER_ERROR"}
	startupCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := r.call(startupCtx, "initialize", map[string]any{
		"clientInfo":   map[string]any{"name": "threavia", "title": "Threavia", "version": c.options.Version},
		"capabilities": map[string]any{"experimentalApi": true},
	}, nil); err != nil {
		out.err = err
		return out
	}
	if err := r.send(map[string]any{"method": "initialized", "params": map[string]any{}}); err != nil {
		out.err = err
		return out
	}
	if c.options.APIKey != "" {
		// The key stays on the backend and travels over stdin, never argv or
		// the Core connection. Explicit API-key configuration selects this auth.
		if err := r.call(startupCtx, "account/login/start", map[string]any{"type": "apiKey", "apiKey": c.options.APIKey}, nil); err != nil {
			out.code = "AUTHENTICATION_REQUIRED"
			out.err = err
			return out
		}
	} else {
		var account struct {
			Account            json.RawMessage `json:"account"`
			RequiresOpenAIAuth bool            `json:"requiresOpenaiAuth"`
		}
		if err := r.call(startupCtx, "account/read", map[string]any{"refreshToken": false}, &account); err != nil {
			out.err = err
			return out
		}
		if account.RequiresOpenAIAuth && (len(account.Account) == 0 || string(account.Account) == "null") {
			out.code = "AUTHENTICATION_REQUIRED"
			out.err = errors.New("authenticate with codex login on the backend machine, or configure OPENAI_API_KEY")
			return out
		}
	}
	// Keep all mutations behind the host permission gate, including autonomous
	// runs: autonomous approval is decided from the current Threavia policy,
	// never by switching Codex to an unrestricted sandbox.
	config := map[string]any{
		"mcp_servers": map[string]any{"threavia": map[string]any{"url": url, "required": true, "tool_timeout_sec": 2147483647}},
		"web_search":  "disabled", "features.hooks": true,
		// Sub-agents run in this process, under this Job's hook and approvals.
		"features.multi_agent": true,
		"features.apps":        false, "features.plugins": false, "features.goals": false,
		"features.browser_use": false, "features.computer_use": false,
		"features.shell_snapshot": false,
		// Background commands, without the terminal that would let a later
		// write to stdin act unseen (see New).
		"features.unified_exec": true, "features.unified_exec_tty": false,
		"features.code_mode": false, "allow_login_shell": false,
	}
	// Disable every local MCP server before adding the per-Job endpoint. A
	// backend must not inherit project knowledge or tools from another context.
	type layerConfig struct {
		MCPServers map[string]any `json:"mcp_servers"`
		Hooks      map[string]any `json:"hooks"`
	}
	var machine struct {
		Config struct {
			MCPServers map[string]any `json:"mcp_servers"`
			AutoReview struct {
				Policy string `json:"policy"`
			} `json:"auto_review"`
		} `json:"config"`
		Layers []struct {
			Config layerConfig `json:"config"`
			Name   struct {
				Type           string `json:"type"`
				File           string `json:"file"`
				DotCodexFolder string `json:"dotCodexFolder"`
			} `json:"name"`
			DisabledReason *string `json:"disabledReason"`
		} `json:"layers"`
	}
	if err := r.call(startupCtx, "config/read", map[string]any{"cwd": p.WorkingDirectory, "includeLayers": true}, &machine); err != nil {
		out.err = err
		return out
	}
	live.mu.Lock()
	live.reviewPolicy = machine.Config.AutoReview.Policy
	live.mu.Unlock()
	for name := range machine.Config.MCPServers {
		config["mcp_servers."+name+".enabled"] = false
	}
	for _, layer := range machine.Layers {
		if layer.DisabledReason != nil {
			continue
		}
		directory := layer.Name.DotCodexFolder
		if layer.Name.File != "" {
			directory = filepath.Dir(layer.Name.File)
		}
		if directory != "" {
			found, err := hasNativeRules(directory)
			if err != nil {
				out.err = err
				return out
			}
			live.mu.Lock()
			live.nativeRules = live.nativeRules || found
			live.mu.Unlock()
		}
		for name := range layer.Config.MCPServers {
			config["mcp_servers."+name+".enabled"] = false
		}
	}
	config["mcp_servers.threavia.enabled"] = true
	if found, err := nativeRulesPresent(p.WorkingDirectory); err != nil {
		out.err = err
		return out
	} else {
		live.mu.Lock()
		live.nativeRules = live.nativeRules || found
		live.mu.Unlock()
	}
	hookTrust, err := verifyHooks(startupCtx, r, p.WorkingDirectory, live.hookCommand)
	if err != nil {
		out.code, out.err = "POLICY_UNSUPPORTED", err
		return out
	}
	// Trust only the two exact host-generated definitions, scoped to this
	// thread. No global trust bypass or durable user configuration changes.
	config["hooks.state"] = hookTrust
	skills, err := loadSkills(startupCtx, r, p.WorkingDirectory, p.SkillDirectory, config)
	if err != nil {
		out.code, out.err = "SKILLS_UNAVAILABLE", err
		return out
	}
	live.mu.Lock()
	live.skills, live.helperConfig = skills, cloneInput(config)
	live.mu.Unlock()
	approval, reviewer := nativeApproval(p.Policy)
	args := map[string]any{"cwd": p.WorkingDirectory, "approvalPolicy": approval, "sandbox": "read-only", "approvalsReviewer": reviewer,
		"developerInstructions": instructions(p, c.available, contract.ScratchDir(c.options.Scratch, p.RunID)), "config": config}
	if c.options.Model != "" {
		args["model"] = c.options.Model
	}
	method := "thread/start"
	if p.NativeSessionID != "" {
		method = "thread/resume"
		args["threadId"] = p.NativeSessionID
	}
	var thread struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := r.call(startupCtx, method, args, &thread); err != nil {
		out.err = err
		return out
	}
	if thread.Thread.ID == "" {
		out.err = errors.New("app-server returned an empty thread id")
		return out
	}
	live.mu.Lock()
	live.threadID = thread.Thread.ID
	live.mu.Unlock()
	if err := sink.JobStarted(reportCtx, p.RunID, p.JobID); err != nil {
		out.err = err
		return out
	}
	if err := sink.NativeSessionBound(reportCtx, p.RunID, p.JobID, thread.Thread.ID); err != nil {
		out.err = err
		return out
	}
	startTurn := func(text string) error {
		live.mu.Lock()
		if live.turnCancel != nil {
			live.turnCancel()
		}
		live.turnCtx, live.turnCancel = context.WithCancel(ctx)
		currentPolicy := live.policy
		live.userMessages = append(live.userMessages, text)
		live.mu.Unlock()
		approval, reviewer := nativeApproval(currentPolicy)
		params := map[string]any{"threadId": thread.Thread.ID, "input": skillInput(supervised(currentPolicy, text), skills),
			"approvalPolicy": approval, "approvalsReviewer": reviewer, "sandboxPolicy": map[string]any{"type": "readOnly", "networkAccess": false}}
		if c.options.ReasoningEffort != "" {
			params["effort"] = c.options.ReasoningEffort
		}
		var response struct {
			Turn struct {
				ID string `json:"id"`
			} `json:"turn"`
		}
		if err := r.call(ctx, "turn/start", params, &response); err != nil {
			return err
		}
		if response.Turn.ID == "" {
			return errors.New("app-server returned an empty turn id")
		}
		live.mu.Lock()
		live.turnID = response.Turn.ID
		live.turns[response.Turn.ID] = true
		live.mu.Unlock()
		return nil
	}
	if err := startTurn(p.Prompt); err != nil {
		out.err = err
		return out
	}
	// Codex can defer SessionStart until the first turn. Confirm the hook ran
	// before processing tool events, rather than assuming thread/start waits
	// for lifecycle hooks. PreToolUse also refuses work until this handshake.
	select {
	case <-live.hookReady:
	case <-startupCtx.Done():
		out.code, out.err = "POLICY_UNSUPPORTED", errors.New("Codex did not execute the Threavia policy hook")
		return out
	}
	actions, plans := 0, 0
	root := thread.Thread.ID
	for {
		var msg message
		select {
		case msg = <-r.events:
		case <-live.policyUpdates:
			// Revoke any pending automatic decision made under the old policy.
			// The replacement turn reads the current policy and supervision.
			if err := c.Inject(p.JobID, "The execution policy has changed. Continue the requested task under the current policy and standing instructions.", true); err != nil {
				out.err = fmt.Errorf("apply updated supervision: %w", err)
				return out
			}
			continue
		case <-ctx.Done():
			out.err = ctx.Err()
			return out
		case <-r.done:
			select {
			case msg = <-r.events:
			default:
				out.err = r.err
				return out
			}
		}
		var payload map[string]any
		if len(msg.Params) > 0 {
			if err := json.Unmarshal(msg.Params, &payload); err != nil {
				out.err = fmt.Errorf("decode %s: %w", msg.Method, err)
				return out
			}
		}
		if msg.Method == "account/rateLimits/updated" {
			_ = contract.ReportQuotas(reportCtx, sink, codexRateLimits(payload), false)
			continue
		}
		// Isolated reviewers/search helpers have their own consumers. Their
		// messages never become user-facing messages or replace the native Run ID.
		threadID := str(payload, "threadId")
		live.mu.Lock()
		helper := live.helpers[threadID]
		live.mu.Unlock()
		if helper != nil {
			if len(msg.ID) > 0 {
				_ = r.respond(msg.ID, nil, errors.New("isolated helper cannot request local permissions"))
				continue
			}
			select {
			case helper.events <- msg:
			case <-helper.done:
			case <-ctx.Done():
				out.err = ctx.Err()
				return out
			}
			continue
		}
		// The root thread and the sub-agents it spawned. A thread that cannot
		// be traced back to this Job must not pollute it, nor be answered.
		sub := c.childOf(ctx, r, live, root, threadID)
		if threadID != "" && threadID != root && sub == nil {
			if len(msg.ID) > 0 {
				_ = r.respond(msg.ID, nil, errors.New("request belongs to another thread"))
			}
			continue
		}
		if len(msg.ID) > 0 {
			live.mu.Lock()
			requestCtx, requestCancel := context.WithCancel(live.turnCtx)
			live.mu.Unlock()
			key := string(msg.ID)
			live.mu.Lock()
			live.requests[key] = requestCancel
			live.mu.Unlock()
			requests.Add(1)
			go func(msg message, payload map[string]any) {
				defer requests.Done()
				defer requestCancel()
				result, err := c.answer(requestCtx, live, p.JobID, msg.Method, payload)
				_ = r.respond(msg.ID, result, err)
				live.mu.Lock()
				delete(live.requests, key)
				live.mu.Unlock()
			}(msg, payload)
			continue
		}
		switch msg.Method {
		case "serverRequest/resolved":
			keyBytes, _ := json.Marshal(payload["requestId"])
			live.mu.Lock()
			if cancel := live.requests[string(keyBytes)]; cancel != nil {
				cancel()
			}
			live.mu.Unlock()
		case "item/agentMessage/delta", "item/reasoning/summaryTextDelta", "item/commandExecution/outputDelta":
			sink.Progress(reportCtx, p.RunID, p.JobID, "provider_output", str(payload, "delta"))
		case "item/started", "item/completed":
			item, _ := payload["item"].(map[string]any)
			id := str(item, "id")
			kind := str(item, "type")
			live.mu.Lock()
			refused := live.refusedItems[id]
			live.items[id] = item
			live.mu.Unlock()
			if refused {
				continue
			}
			switch kind {
			case "agentMessage":
				text := str(item, "text")
				if msg.Method != "item/completed" || strings.TrimSpace(text) == "" {
					continue
				}
				if sub != nil {
					// A sub-agent answers the agent that spawned it, not the user:
					// its last word is the result of its Task call.
					live.mu.Lock()
					sub.message = text
					live.mu.Unlock()
					sink.Progress(reportCtx, p.RunID, p.JobID, "provider_output", text)
					continue
				}
				if err := sink.AgentMessage(reportCtx, p.RunID, p.JobID, text); err != nil {
					out.err = err
					return out
				}
				out.summary = text
				continue
			case "subAgentActivity":
				if msg.Method != "item/completed" {
					continue
				}
				if err := c.subAgentActivity(reportCtx, live, p, item, &actions); err != nil {
					if errors.Is(err, errActionLimit) {
						out.code = "POLICY_LIMIT"
					}
					out.err = err
					return out
				}
				continue
			case "contextCompaction":
				if msg.Method == "item/completed" {
					sink.Progress(reportCtx, p.RunID, p.JobID, "agent.working", "context compacted")
				}
				continue
			}
			name, input := tool(item)
			if name == "" {
				continue
			}
			if msg.Method == "item/started" {
				actions++
				if p.Policy.MaxActions > 0 && actions > p.Policy.MaxActions {
					out.code = "POLICY_LIMIT"
					out.err = errActionLimit
					return out
				}
				if err := sink.ToolStarted(reportCtx, p.RunID, p.JobID, id, name, input); err != nil {
					out.err = err
					return out
				}
			} else {
				if detail, failed := toolFailure(item); failed {
					if err := sink.ToolFailed(reportCtx, p.RunID, p.JobID, id, name, detail); err != nil {
						out.err = err
						return out
					}
				} else {
					if err := sink.ToolCompleted(reportCtx, p.RunID, p.JobID, id, name, item); err != nil {
						out.err = err
						return out
					}
				}
			}
		case "turn/plan/updated":
			// The sub-agents' plans are their own business, as their messages are.
			if sub != nil {
				continue
			}
			plans++
			if err := planUpdated(reportCtx, sink, p, payload, plans); err != nil {
				out.err = err
				return out
			}
		case "turn/started":
			turn, _ := payload["turn"].(map[string]any)
			live.mu.Lock()
			live.turns[str(turn, "id")] = true
			if sub != nil {
				sub.turnID = str(turn, "id")
			}
			live.mu.Unlock()
		case "thread/tokenUsage/updated":
			// One update per model request, "last" being that request and
			// "total" the thread's whole history. A Job is charged for the
			// requests of the turns it ran, root and sub-agents alike, never
			// for the earlier turns of a resumed conversation.
			usage, _ := payload["tokenUsage"].(map[string]any)
			live.mu.Lock()
			ours := live.turns[str(payload, "turnId")]
			live.mu.Unlock()
			if !ours {
				continue
			}
			last, _ := usage["last"].(map[string]any)
			out.usage = addUsage(out.usage, decodeUsage(last))
		case "guardianWarning", "autoApprovalReview/strictReviewRequired", "item/autoApprovalReview/started", "item/autoApprovalReview/completed":
			sink.Progress(reportCtx, p.RunID, p.JobID, "supervision", str(payload, "message"))
		case "turn/completed":
			turn, _ := payload["turn"].(map[string]any)
			if sub != nil {
				// What the sub-agent did is reported by its activity in the
				// thread that spawned it; here it only stops being interruptible.
				live.mu.Lock()
				if sub.turnID == str(turn, "id") {
					sub.turnID = ""
				}
				live.mu.Unlock()
				continue
			}
			live.mu.Lock()
			live.approvals = nil
			if live.turnCancel != nil {
				live.turnCancel()
			}
			// A turn cannot retain a question after it has ended.
			for _, cancel := range live.requests {
				cancel()
			}
			var next *queuedInput
			if len(live.queue) > 0 {
				next = live.queue[0]
				live.queue = live.queue[1:]
			} else {
				live.finishing = true
			}
			live.mu.Unlock()
			status := str(turn, "status")
			if status == "failed" {
				failure, _ := turn["error"].(map[string]any)
				out.err = errors.New(str(failure, "message"))
				return out
			}
			if next != nil {
				if err := startTurn(next.text); err != nil {
					out.err = err
					return out
				}
				continue
			}
			if status != "completed" {
				out.err = fmt.Errorf("Codex turn ended with status %q", status)
			}
			return out
		}
	}
}

// Native allow rules can skip app-server approvals. Detect them so the
// PreToolUse hook applies the full host gate before a tool executes.
func hasNativeRules(directory string) (bool, error) {
	root := filepath.Join(directory, "rules")
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect Codex rules: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".rules" {
			return true, nil
		}
	}
	return false, nil
}

func str(m map[string]any, key string) string { value, _ := m[key].(string); return value }
func toolFailure(item map[string]any) (string, bool) {
	status := str(item, "status")
	result, _ := item["result"].(map[string]any)
	failed := status == "failed" || status == "declined" || item["error"] != nil || result["isError"] == true
	if exit, ok := item["exitCode"].(float64); ok && exit != 0 {
		failed = true
	}
	if !failed {
		return "", false
	}
	detail := str(item, "aggregatedOutput")
	if detail == "" && item["error"] != nil {
		detail = fmt.Sprint(item["error"])
	}
	if detail == "" && result["content"] != nil {
		detail = fmt.Sprint(result["content"])
	}
	if detail == "" {
		detail = "Tool ended with status " + status
	}
	return detail, true
}
func tool(item map[string]any) (string, map[string]any) {
	switch str(item, "type") {
	case "commandExecution":
		return "Bash", map[string]any{"command": item["command"], "cwd": item["cwd"]}
	case "fileChange":
		return "Edit", map[string]any{"changes": item["changes"]}
	case "mcpToolCall":
		input, _ := item["arguments"].(map[string]any)
		if str(item, "server") == "threavia" {
			if str(item, "tool") == "web_search" {
				return "WebSearch", input
			}
			if str(item, "tool") == "web_fetch" {
				return "WebFetch", input
			}
			if str(item, "tool") == "update_plan" {
				return "TodoWrite", todoInput(input)
			}
		}
		return "mcp__" + str(item, "server") + "__" + str(item, "tool"), input
	case "webSearch":
		return "WebSearch", item
	case "imageView":
		return "Read", map[string]any{"file_path": item["path"]}
	default:
		return "", nil
	}
}
func token(m map[string]any, key string) uint64 {
	n, _ := m[key].(float64)
	if n <= 0 {
		return 0
	}
	return uint64(n)
}
func decodeUsage(m map[string]any) *backendv1.Usage {
	if m == nil {
		return nil
	}
	cached := token(m, "cachedInputTokens")
	input := token(m, "inputTokens")
	if input >= cached {
		input -= cached
	}
	return &backendv1.Usage{InputTokens: input, OutputTokens: token(m, "outputTokens"), CacheReadTokens: cached, CacheWriteTokens: token(m, "cacheWriteInputTokens")}
}
func addUsage(a, b *backendv1.Usage) *backendv1.Usage {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	return &backendv1.Usage{InputTokens: a.InputTokens + b.InputTokens, OutputTokens: a.OutputTokens + b.OutputTokens, CacheReadTokens: a.CacheReadTokens + b.CacheReadTokens, CacheWriteTokens: a.CacheWriteTokens + b.CacheWriteTokens}
}
