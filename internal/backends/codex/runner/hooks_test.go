package runner

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/backends/shared/policy"
)

func TestFineRulesBeforeNativeExecution(t *testing.T) {
	c, p, _ := provider(t, "success")
	asker := &testAsker{approved: true}
	c.SetAsker(asker)
	live := &execution{ctx: context.Background(), cwd: p.WorkingDirectory, policy: p.Policy}
	live.policy.Rules = []policy.Rule{
		{Effect: backendv1.PermissionEffect_PERMISSION_EFFECT_ALLOW, Capability: backendv1.PermissionCapability_PERMISSION_CAPABILITY_SHELL, Match: "go test *"},
		{Effect: backendv1.PermissionEffect_PERMISSION_EFFECT_ASK, Capability: backendv1.PermissionCapability_PERMISSION_CAPABILITY_SHELL, Match: "git status*"},
		{Effect: backendv1.PermissionEffect_PERMISSION_EFFECT_DENY, Capability: backendv1.PermissionCapability_PERMISSION_CAPABILITY_FILE_READ, Match: "secrets/**"},
		{Effect: backendv1.PermissionEffect_PERMISSION_EFFECT_DENY, Capability: backendv1.PermissionCapability_PERMISSION_CAPABILITY_FILE_WRITE, Match: "locked/*"},
	}
	for _, tt := range []struct {
		command string
		deny    bool
		asks    int
	}{
		{"go test ./...", false, 0}, {"cat secrets/key", true, 0}, {"git status", false, 1},
	} {
		result, err := c.preTool(context.Background(), live, "job", map[string]any{"hook_event_name": "PreToolUse", "tool_name": "Bash", "tool_input": map[string]any{"command": tt.command}})
		if err != nil || (result.(map[string]any)["decision"] == "block") != tt.deny || len(asker.calls) != tt.asks {
			t.Fatalf("%s: %v, %v, prompts %v", tt.command, result, err, asker.calls)
		}
	}
	result, err := c.preTool(context.Background(), live, "job", map[string]any{"hook_event_name": "PreToolUse", "tool_name": "apply_patch", "tool_input": map[string]any{"command": "*** Begin Patch\n*** Update File: src/a.go\n*** Move to: locked/a.go\n@@\n-x\n+y\n*** End Patch"}})
	if err != nil || result.(map[string]any)["decision"] != "block" {
		t.Fatalf("patch move bypassed its destination rule: %v %v", result, err)
	}
	// A subsequent native approval for a command already reviewed must consume
	// that exact approval once, without asking the user a second time.
	if _, err := c.decide(context.Background(), live, "job", "Bash", map[string]any{"command": "git status"}, false); err != nil || len(asker.calls) != 1 {
		t.Fatalf("duplicate native prompt: %v, %v", asker.calls, err)
	}
}

func TestWrappedNativeApprovalConsumesHookReceipt(t *testing.T) {
	c, p, _ := provider(t, "success")
	asker := &testAsker{approved: true}
	c.SetAsker(asker)
	live := &execution{ctx: context.Background(), cwd: p.WorkingDirectory, policy: p.Policy}
	live.policy.Rules = []policy.Rule{{Effect: backendv1.PermissionEffect_PERMISSION_EFFECT_ASK, Capability: backendv1.PermissionCapability_PERMISSION_CAPABILITY_SHELL, Match: "touch *"}}
	_, err := c.preTool(context.Background(), live, "job", map[string]any{"hook_event_name": "PreToolUse", "tool_name": "Bash", "tool_use_id": "cmd-1", "tool_input": map[string]any{"command": "touch marker.txt"}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := c.answer(context.Background(), live, "job", "item/commandExecution/requestApproval", map[string]any{"itemId": "cmd-1", "command": "/bin/zsh -c 'touch marker.txt'"})
	if err != nil || result.(map[string]any)["decision"] != "accept" || len(asker.calls) != 1 {
		t.Fatal("native shell wrapper caused a duplicate validation", result, err, asker.calls)
	}
}

func TestOnlySearchHelperCanUseHostedSearch(t *testing.T) {
	c, p, _ := provider(t, "success")
	live := &execution{ctx: context.Background(), cwd: p.WorkingDirectory, policy: p.Policy, helpers: map[string]*helperStream{"search": {webSearch: true}, "review": {}}}
	for _, tt := range []struct {
		helper, tool string
		input        map[string]any
		allow        bool
	}{
		{"search", "webrun", map[string]any{"search_query": []any{map[string]any{"q": "docs"}}}, true},
		{"search", "webrun", map[string]any{"open": []any{map[string]any{"ref_id": "https://example.com"}}}, false},
		{"search", "Bash", map[string]any{"command": "cat secret"}, false},
		{"review", "webrun", map[string]any{"search_query": []any{map[string]any{"q": "docs"}}}, false},
	} {
		result, err := c.preTool(context.Background(), live, "job", map[string]any{"hook_event_name": "PreToolUse", "session_id": tt.helper, "tool_name": tt.tool, "tool_input": tt.input})
		if err != nil || (result.(map[string]any)["decision"] != "block") != tt.allow {
			t.Fatal("isolated helper tool permissions", tt, result, err)
		}
	}
}

func TestRefusalsAreRecordedOnceAndHelpersStayPrivate(t *testing.T) {
	c, p, sink := provider(t, "success")
	live := &execution{ctx: context.Background(), reportCtx: context.Background(), sink: sink, runID: "run", jobID: "job", cwd: p.WorkingDirectory, policy: p.Policy, helpers: map[string]*helperStream{"review": {}}}
	live.policy.Rules = []policy.Rule{{Effect: backendv1.PermissionEffect_PERMISSION_EFFECT_DENY, Capability: backendv1.PermissionCapability_PERMISSION_CAPABILITY_SHELL, Match: "cat *"}}
	event := map[string]any{"hook_event_name": "PreToolUse", "session_id": "root", "tool_name": "Bash", "tool_use_id": "denied-call", "tool_input": map[string]any{"command": "cat secret"}}
	for range 2 {
		result, err := c.preTool(context.Background(), live, "job", event)
		if err != nil || result.(map[string]any)["decision"] != "block" {
			t.Fatal("denied action was allowed", result, err)
		}
	}
	if len(sink.events) != 2 || sink.events[0] != "tool:Bash:denied-call" || sink.events[1] != "tool-failed:Bash:denied-call" {
		t.Fatal("refusal was missing or duplicated", sink.events)
	}
	event["session_id"], event["tool_use_id"] = "review", "private-call"
	_, _ = c.preTool(context.Background(), live, "job", event)
	if len(sink.events) != 2 {
		t.Fatal("private reviewer tool leaked into user events", sink.events)
	}
}

func TestFinePolicyUpdateRemainsActive(t *testing.T) {
	c, p, sink := provider(t, "hang")
	sink.started = make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- c.Run(context.Background(), p, sink) }()
	select {
	case <-sink.started:
	case <-time.After(5 * time.Second):
		t.Fatal("job did not start")
	}
	p.Policy.Rules = []policy.Rule{{Effect: backendv1.PermissionEffect_PERMISSION_EFFECT_DENY, Capability: backendv1.PermissionCapability_PERMISSION_CAPABILITY_SHELL, Match: "rm *"}}
	c.UpdatePolicy(p.JobID, p.Policy)
	c.mu.Lock()
	live := c.running[p.JobID]
	c.mu.Unlock()
	d, err := c.decide(context.Background(), live, p.JobID, "Bash", map[string]any{"command": "rm file"}, true)
	if err != nil || d.Approved {
		t.Fatalf("updated denial ignored: %+v %v", d, err)
	}
	if err := c.Cancel(p.JobID); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if sink.code != "" || sink.events[len(sink.events)-1] != "cancelled" {
		t.Fatalf("a supported update failed the Job: %+v", sink)
	}
}

func TestCoreToolRulesAndMandatoryValidation(t *testing.T) {
	c, p, _ := provider(t, "success")
	asker := &testAsker{approved: true}
	c.SetAsker(asker)
	live := &execution{ctx: context.Background(), cwd: p.WorkingDirectory, policy: p.Policy}
	event := map[string]any{"hook_event_name": "PreToolUse", "tool_name": "mcp__threavia__directory_set", "tool_input": map[string]any{}, "threavia_direct": true, "requires_validation": true}
	result, err := c.preTool(context.Background(), live, "job", event)
	if err != nil || result.(map[string]any)["decision"] == "block" || len(asker.calls) != 1 {
		t.Fatalf("mandatory validation missing: %v %v", result, err)
	}
	live.policy.Rules = []policy.Rule{{Effect: backendv1.PermissionEffect_PERMISSION_EFFECT_DENY, Capability: backendv1.PermissionCapability_PERMISSION_CAPABILITY_TOOL, Match: "mcp__threavia__directory_set"}}
	result, err = c.preTool(context.Background(), live, "job", event)
	if err != nil || result.(map[string]any)["decision"] != "block" || len(asker.calls) != 1 {
		t.Fatalf("Core tool denial ignored: %v %v", result, err)
	}
}

func TestPolicyChangeInvalidatesPendingApproval(t *testing.T) {
	c, p, _ := provider(t, "success")
	release := make(chan struct{})
	asker := &testAsker{approved: true, called: make(chan struct{}, 1), release: release}
	c.SetAsker(asker)
	live := &execution{ctx: context.Background(), cwd: p.WorkingDirectory, policy: p.Policy}
	live.policy.Mode = backendv1.ExecutionMode_EXECUTION_MODE_INTERACTIVE
	c.running["job"] = live
	done := make(chan bool, 1)
	go func() {
		d, _ := c.decide(context.Background(), live, "job", "Bash", map[string]any{"command": "touch file"}, true)
		done <- d.Approved
	}()
	<-asker.called
	p.Policy.Rules = []policy.Rule{{Effect: backendv1.PermissionEffect_PERMISSION_EFFECT_DENY, Capability: backendv1.PermissionCapability_PERMISSION_CAPABILITY_SHELL, Match: "touch *"}}
	c.UpdatePolicy("job", p.Policy)
	close(release)
	if <-done {
		t.Fatal("old human approval overrode the new policy")
	}
}

func TestPatchRejectsUnknownShape(t *testing.T) {
	for _, patch := range []string{"", "echo file", "*** Begin Patch\n*** Update File: \n*** End Patch"} {
		if paths, err := patchPaths(patch); err == nil {
			b, _ := json.Marshal(paths)
			t.Fatalf("unknown patch accepted: %s", b)
		}
	}
}

func TestMissingOrConflictingHooksStopBeforeInference(t *testing.T) {
	for _, scenario := range []string{"missing-hooks"} {
		c, p, sink := provider(t, scenario)
		if err := c.Run(context.Background(), p, sink); err != nil {
			t.Fatal(err)
		}
		if sink.code != "POLICY_UNSUPPORTED" || sink.native != "" {
			t.Fatalf("%s started unsafe execution: %+v", scenario, sink)
		}
	}
}

func TestFineRulesAreAcceptedAtJobStart(t *testing.T) {
	c, p, sink := provider(t, "success")
	p.Policy.Rules = []policy.Rule{{Effect: backendv1.PermissionEffect_PERMISSION_EFFECT_DENY, Capability: backendv1.PermissionCapability_PERMISSION_CAPABILITY_FILE_READ, Match: "secrets/**"}}
	if err := c.Run(context.Background(), p, sink); err != nil {
		t.Fatal(err)
	}
	if sink.code != "" || sink.native == "" {
		t.Fatalf("a supported policy failed: %+v", sink)
	}
}

func TestLocalHooksDisabledOnlyForJob(t *testing.T) {
	c, p, sink := provider(t, "local-hooks")
	if err := c.Run(context.Background(), p, sink); err != nil {
		t.Fatal(err)
	}
	if sink.code != "" || sink.native == "" {
		t.Fatalf("local hooks blocked the Job: %+v", sink)
	}
}

func TestInterruptCancelsHookValidation(t *testing.T) {
	c, p, _ := provider(t, "success")
	asker := &testAsker{wait: true, called: make(chan struct{}, 1)}
	c.SetAsker(asker)
	turnCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	live := &execution{ctx: context.Background(), turnCtx: turnCtx, cwd: p.WorkingDirectory, policy: p.Policy}
	live.policy.Mode = backendv1.ExecutionMode_EXECUTION_MODE_INTERACTIVE
	done := make(chan any, 1)
	go func() {
		result, _ := c.preTool(context.Background(), live, "job", map[string]any{"hook_event_name": "PreToolUse", "tool_name": "Bash", "tool_input": map[string]any{"command": "pwd"}})
		done <- result
	}()
	select {
	case <-asker.called:
	case <-time.After(time.Second):
		t.Fatal("hook did not request validation")
	}
	cancel()
	select {
	case result := <-done:
		if result.(map[string]any)["decision"] != "block" {
			t.Fatal("interrupted hook allowed execution")
		}
	case <-time.After(time.Second):
		t.Fatal("interrupted hook kept waiting")
	}
}
