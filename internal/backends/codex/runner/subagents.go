package runner

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/backends/shared/mcp"
	"github.com/rclsilver/threavia/internal/backends/shared/policy"
	contract "github.com/rclsilver/threavia/internal/backends/shared/runner"
)

// Sub-agents are threads Codex spawns from the Job's thread, in the same
// app-server process. Their events arrive with their own thread id and no
// thread/started of their own; their tool calls reach the policy hook under
// the root session, and their approvals reach this backend like the root's.
// What makes them part of the Job is their ancestry, which is checked rather
// than assumed: a thread id the Job cannot trace back to its own is refused.

// childOf returns the sub-agent thread for id, registering it when Codex says
// its parent is this Job's thread or one of its children. Nil for the root,
// for helpers, and for a thread that belongs to nothing here.
func (c *Codex) childOf(ctx context.Context, r *rpc, live *execution, root, id string) *child {
	if id == "" || id == root {
		return nil
	}
	live.mu.Lock()
	known, foreign := live.children[id], live.foreign[id]
	live.mu.Unlock()
	if known != nil || foreign {
		return known
	}
	readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var response struct {
		Thread struct {
			ParentThreadID string `json:"parentThreadId"`
		} `json:"thread"`
	}
	if err := r.call(readCtx, "thread/read", map[string]any{"threadId": id}, &response); err != nil {
		// Not remembered as foreign: a thread being created may not be
		// readable yet, and the next event will ask again.
		c.logger.Debug("cannot read the parent of a Codex thread", "thread", id, "error", err)
		return nil
	}
	parent := response.Thread.ParentThreadID
	live.mu.Lock()
	defer live.mu.Unlock()
	if parent == root || (parent != "" && live.children[parent] != nil) {
		return live.registerChild(id)
	}
	live.foreign[id] = true
	return nil
}

// registerChild records a sub-agent thread. Called with live.mu held.
func (live *execution) registerChild(id string) *child {
	if existing := live.children[id]; existing != nil {
		return existing
	}
	created := &child{}
	live.children[id] = created
	return created
}

// subAgentActivity turns Codex's account of a sub-agent into one Task tool
// call per stretch of work: opened when it starts or is given more to do,
// closed with its last message when it completes, failed when interrupted.
// It reports whether the action limit was reached.
func (c *Codex) subAgentActivity(reportCtx context.Context, live *execution, p contract.StartParams, item map[string]any, actions *int) error {
	threadID := str(item, "agentThreadId")
	if threadID == "" {
		return nil
	}
	live.mu.Lock()
	sub := live.registerChild(threadID)
	open := sub.call
	live.mu.Unlock()
	sink := live.sink
	switch str(item, "kind") {
	case "started", "interacted":
		if open != "" {
			return nil
		}
		*actions++
		if p.Policy.MaxActions > 0 && *actions > p.Policy.MaxActions {
			return errActionLimit
		}
		id := str(item, "id")
		live.mu.Lock()
		sub.call, sub.message = id, ""
		live.mu.Unlock()
		agentPath := str(item, "agentPath")
		return sink.ToolStarted(reportCtx, p.RunID, p.JobID, id, "Task", map[string]any{
			"description": path.Base(agentPath), "agent_path": agentPath, "agent_thread_id": threadID,
		})
	case "completed", "interrupted":
		if open == "" {
			return nil
		}
		live.mu.Lock()
		message := sub.message
		sub.call = ""
		live.mu.Unlock()
		if str(item, "kind") == "interrupted" {
			return sink.ToolFailed(reportCtx, p.RunID, p.JobID, open, "Task", "The sub-agent was interrupted.")
		}
		return sink.ToolCompleted(reportCtx, p.RunID, p.JobID, open, "Task", map[string]any{"output": message})
	}
	return nil
}

var errActionLimit = fmt.Errorf("execution action limit reached")

// closeChildren fails every Task call still open, once nothing more will be
// heard from the sub-agents behind them.
func (c *Codex) closeChildren(ctx context.Context, live *execution, reason string) {
	live.mu.Lock()
	open := []string{}
	for _, sub := range live.children {
		if sub.call != "" {
			open = append(open, sub.call)
			sub.call = ""
		}
	}
	live.mu.Unlock()
	for _, id := range open {
		if err := live.sink.ToolFailed(ctx, live.runID, live.jobID, id, "Task", reason); err != nil {
			c.logger.Warn("cannot report a stopped sub-agent", "error", err)
		}
	}
}

// interruptChildren stops the turns sub-agents have under way.
func interruptChildren(ctx context.Context, r *rpc, live *execution) {
	live.mu.Lock()
	turns := map[string]string{}
	for id, sub := range live.children {
		if sub.turnID != "" {
			turns[id] = sub.turnID
		}
	}
	live.mu.Unlock()
	for threadID, turnID := range turns {
		_ = r.call(ctx, "turn/interrupt", map[string]any{"threadId": threadID, "turnId": turnID}, nil)
	}
}

// cleanBackground stops the commands the Job's threads left running.
func cleanBackground(ctx context.Context, r *rpc, live *execution) {
	live.mu.Lock()
	threads := []string{}
	if live.threadID != "" {
		threads = append(threads, live.threadID)
	}
	for id := range live.children {
		threads = append(threads, id)
	}
	live.mu.Unlock()
	for _, id := range threads {
		_ = r.call(ctx, "thread/backgroundTerminals/clean", map[string]any{"threadId": id}, nil)
	}
}

// planUpdated reports Codex's plan the way Claude Code reports its todo list:
// as a TodoWrite call carrying every step and its status, so a client renders
// either provider's plan the same way.
func planUpdated(reportCtx context.Context, sink contract.Sink, p contract.StartParams, payload map[string]any, sequence int) error {
	input := todoInput(payload)
	id := fmt.Sprintf("plan-%s-%d", str(payload, "turnId"), sequence)
	if err := sink.ToolStarted(reportCtx, p.RunID, p.JobID, id, "TodoWrite", input); err != nil {
		return err
	}
	return sink.ToolCompleted(reportCtx, p.RunID, p.JobID, id, "TodoWrite", map[string]any{"output": "Plan updated."})
}

// todoInput renders a Codex plan, native or recorded with the backend's
// update_plan tool, as the input of Claude Code's TodoWrite.
func todoInput(plan map[string]any) map[string]any {
	steps, _ := plan["plan"].([]any)
	todos := make([]any, 0, len(steps))
	for _, entry := range steps {
		step, _ := entry.(map[string]any)
		status := str(step, "status")
		if status == "inProgress" {
			status = "in_progress"
		}
		todos = append(todos, map[string]any{"content": str(step, "step"), "status": status})
	}
	input := map[string]any{"todos": todos}
	if explanation := strings.TrimSpace(str(plan, "explanation")); explanation != "" {
		input["explanation"] = explanation
	}
	return input
}

// planTool lets the agent keep a plan the user can follow. The CLI's own plan
// tool is not offered to every model, and Claude Code always has TodoWrite.
// Its call is reported as TodoWrite; only a rule written about TodoWrite
// applies to it, as for Claude Code.
func (c *Codex) planTool(live *execution, job string) mcp.LocalTool {
	status := map[string]any{"type": "string", "enum": []string{"pending", "in_progress", "completed"}}
	step := map[string]any{"type": "object", "required": []string{"step", "status"}, "properties": map[string]any{"step": map[string]any{"type": "string"}, "status": status}}
	return mcp.LocalTool{
		Name:        "update_plan",
		Description: "Record or update your plan for the task: every step, with its status (pending, in_progress, completed). Use it for work of several steps and keep it current as you go; the user follows it. At most one step is in_progress.",
		InputSchema: map[string]any{"type": "object", "required": []string{"plan"}, "properties": map[string]any{"plan": map[string]any{"type": "array", "items": step}, "explanation": map[string]any{"type": "string"}}},
		Call: func(ctx context.Context, input map[string]any) (any, error) {
			if allowed, reason := c.explicitOnly(ctx, live, job, "TodoWrite", todoInput(input), false); !allowed {
				if reason == "" {
					reason = "The execution policy refuses updating the plan."
				}
				return nil, errors.New(reason)
			}
			return "Plan updated.", nil
		},
	}
}

// controlTool names, in the vocabulary rules are written in, a Codex tool that
// coordinates the agent's own work rather than acting on the machine. Claude
// Code runs its equivalents without asking in every mode; only an explicit
// rule about them applies.
func controlTool(name string) (string, bool) {
	switch {
	case strings.HasPrefix(name, "collaboration"):
		return "Task", true
	case name == "update_plan":
		return "TodoWrite", true
	}
	return "", false
}

// explicitOnly applies only the rules written about a tool, and the
// validation Core requires for it: the tool itself is not an action the mode
// asks about.
func (c *Codex) explicitOnly(ctx context.Context, live *execution, jobID, tool string, input map[string]any, mandatory bool) (bool, string) {
	live.mu.Lock()
	p, version, cwd := live.policy, live.policyVersion, live.cwd
	live.mu.Unlock()
	p.Mode = backendv1.ExecutionMode_EXECUTION_MODE_AUTONOMOUS
	d := invocationDecision(p, tool, input, cwd)
	if d.Verdict == policy.Deny {
		return false, d.Reason
	}
	if d.Verdict == policy.Allow && !mandatory {
		return true, ""
	}
	if c.asker == nil {
		return false, "Permission gate is not configured."
	}
	approved, err := c.asker.AskValidation(ctx, jobID, tool, input)
	if err != nil || !approved.Approved {
		return false, approved.Reason
	}
	live.mu.Lock()
	changed := live.policyVersion != version
	live.mu.Unlock()
	if changed || ctx.Err() != nil {
		return false, "The policy changed or the turn ended while awaiting approval."
	}
	return true, ""
}
