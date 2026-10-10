package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/backends/shared/policy"
	contract "github.com/rclsilver/threavia/internal/backends/shared/runner"
)

// A sub-agent's work is the Job's: its commands are approved and reported, its
// result closes a Task call, its tokens are charged once, and a thread the Job
// cannot trace back to its own is refused.
func TestSubAgentsPlansAndUsage(t *testing.T) {
	record := filepath.Join(t.TempDir(), "record")
	t.Setenv("THREAVIA_CODEX_TEST_RECORD", record)
	c, p, sink := provider(t, "subagents")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Run(ctx, p, sink); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(sink.events, ",")
	for _, want := range []string{
		"tool:Task:spawn-1,tool:Bash:child-cmd,result:Bash:child-cmd,progress:alpha written,result:Task:spawn-1",
		"tool:TodoWrite:plan-turn-1-1,result:TodoWrite:plan-turn-1-1",
		"message:done,completed",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in %s", want, joined)
		}
	}
	if strings.Contains(joined, "message:alpha written") {
		t.Fatal("a sub-agent's answer was reported as the agent's message to the user")
	}
	if sink.summary != "done" {
		t.Fatalf("the Job's result must be the root agent's: %q", sink.summary)
	}
	if task := sink.payloads["tool:Task:spawn-1"]; task["description"] != "alpha" || task["agent_thread_id"] != "child-1" {
		t.Fatalf("Task input: %v", task)
	}
	if result := sink.payloads["result:Task:spawn-1"]; result["output"] != "alpha written" {
		t.Fatalf("Task output: %v", result)
	}
	todos, _ := sink.payloads["tool:TodoWrite:plan-turn-1-1"]["todos"].([]any)
	if len(todos) != 2 || todos[1].(map[string]any)["status"] != "in_progress" || todos[0].(map[string]any)["content"] != "Spawn alpha" {
		t.Fatalf("plan: %v", sink.payloads["tool:TodoWrite:plan-turn-1-1"])
	}
	// Root 10/1 and 40(+60 cached)/20, child 50/5; never the restored 9999.
	if sink.usage == nil || sink.usage.InputTokens != 100 || sink.usage.OutputTokens != 26 || sink.usage.CacheReadTokens != 60 {
		t.Fatalf("usage: %v", sink.usage)
	}
	cleaned, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	for _, thread := range []string{"thread-1", "child-1"} {
		if !strings.Contains(string(cleaned), "thread/backgroundTerminals/clean "+thread) {
			t.Fatalf("background commands of %s were not stopped: %s", thread, cleaned)
		}
	}
}

// Spawning, waiting for or messaging a sub-agent and updating the plan act on
// nothing: Claude Code runs Task and TodoWrite without asking, even in
// INTERACTIVE. A rule written about them still applies.
func TestControlToolsFollowOnlyExplicitRules(t *testing.T) {
	c, p, _ := provider(t, "success")
	asker := &testAsker{approved: true}
	c.SetAsker(asker)
	p.Policy.Mode = backendv1.ExecutionMode_EXECUTION_MODE_INTERACTIVE
	live := &execution{ctx: context.Background(), cwd: p.WorkingDirectory, policy: p.Policy}
	hook := func(tool string) bool {
		t.Helper()
		result, err := c.preTool(context.Background(), live, "job", map[string]any{"hook_event_name": "PreToolUse", "tool_name": tool, "tool_input": map[string]any{"task_name": "alpha"}})
		if err != nil {
			t.Fatal(err)
		}
		return result.(map[string]any)["decision"] != "block"
	}
	for _, tool := range []string{"collaborationspawn_agent", "collaborationwait_agent", "update_plan"} {
		if !hook(tool) || len(asker.calls) != 0 {
			t.Fatalf("%s: asked %v", tool, asker.calls)
		}
	}
	live.policy.Rules = []policy.Rule{{Effect: backendv1.PermissionEffect_PERMISSION_EFFECT_DENY, Capability: backendv1.PermissionCapability_PERMISSION_CAPABILITY_TOOL, Match: "Task"}}
	if hook("collaborationspawn_agent") {
		t.Fatal("a TOOL rule refusing Task did not refuse a sub-agent")
	}
	if !hook("update_plan") {
		t.Fatal("a rule about Task refused the plan")
	}
	live.policy.Rules = []policy.Rule{{Effect: backendv1.PermissionEffect_PERMISSION_EFFECT_ASK, Capability: backendv1.PermissionCapability_PERMISSION_CAPABILITY_TOOL, Match: "Task"}}
	if !hook("collaborationspawn_agent") || len(asker.calls) != 1 || asker.calls[0] != "Task" {
		t.Fatalf("a TOOL rule asking about Task was not asked: %v", asker.calls)
	}
}

// The backend's plan tool is reported as Claude Code's TodoWrite, and only a
// rule written about TodoWrite applies to it.
func TestPlanToolReportsTodoWrite(t *testing.T) {
	c, p, _ := provider(t, "success")
	asker := &testAsker{approved: true}
	c.SetAsker(asker)
	p.Policy.Mode = backendv1.ExecutionMode_EXECUTION_MODE_INTERACTIVE
	live := &execution{ctx: context.Background(), cwd: p.WorkingDirectory, policy: p.Policy}
	plan := map[string]any{"plan": []any{map[string]any{"step": "Read", "status": "in_progress"}}}
	planner := c.planTool(live, "job")
	if result, err := planner.Call(context.Background(), plan); err != nil || result != "Plan updated." || len(asker.calls) != 0 {
		t.Fatalf("plan: %v %v, asked %v", result, err, asker.calls)
	}
	live.policy.Rules = []policy.Rule{{Effect: backendv1.PermissionEffect_PERMISSION_EFFECT_DENY, Capability: backendv1.PermissionCapability_PERMISSION_CAPABILITY_TOOL, Match: "TodoWrite"}}
	if _, err := planner.Call(context.Background(), plan); err == nil {
		t.Fatal("a rule refusing TodoWrite did not refuse the plan")
	}
	name, input := tool(map[string]any{"type": "mcpToolCall", "server": "threavia", "tool": "update_plan", "arguments": plan})
	todos, _ := input["todos"].([]any)
	if name != "TodoWrite" || len(todos) != 1 || todos[0].(map[string]any)["content"] != "Read" {
		t.Fatalf("reported as %s %v", name, input)
	}
}

// The Codex prompt is the one Claude Code receives, plus what only Codex needs.
func TestInstructionsMatchTheSharedPrompt(t *testing.T) {
	params := contract.StartParams{ProjectName: "homelab", ProjectInstructions: "Never deploy on Fridays.", SkillDirectory: "/skills"}
	text := instructions(params, []string{"kubectl", "tofu"}, "/scratch/run")
	if !strings.HasPrefix(text, contract.SystemPrompt(params, []string{"kubectl", "tofu"}, "/scratch/run")) {
		t.Fatal("the Codex prompt diverged from the shared one")
	}
	for _, want := range []string{"kubectl tofu", "/scratch/run", "Never deploy on Fridays.", "Skills", "web_search", "stdin is closed"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q", want)
		}
	}
}
