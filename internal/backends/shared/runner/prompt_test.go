package runner

import (
	"strings"
	"testing"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/backends/shared/mcp"
)

func TestMemoryRequestsUseProjectToolsInsteadOfLocalFiles(t *testing.T) {
	t.Parallel()

	prompt := SystemPrompt(StartParams{CoreTools: []mcp.CoreTool{{Name: "decision_create"}}}, nil, "")
	for _, want := range []string{
		"remember", "retiens", "project memory tools",
		"takes precedence over provider-native memory",
		"do not create or update local memory files",
		"information was not saved", "never the secret values",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("memory instructions omit %q", want)
		}
	}
	if bare := SystemPrompt(StartParams{}, nil, ""); strings.Contains(bare, "project memory tools") {
		t.Error("a job without Core tools was told to save through unavailable memory tools")
	}
}

func TestSchedulingUsesPersistentThreaviaTools(t *testing.T) {
	for _, params := range []StartParams{{}, {CoreTools: []mcp.CoreTool{{Name: "schedule_create"}}}} {
		prompt := SystemPrompt(params, nil, "")
		for _, want := range []string{"mcp__threavia__schedule_list", "schedule_create", "schedule_update", "schedule_delete", "busy", "offline", "never accumulated or sent late", "CronCreate", "ScheduleWakeup", "tools are unavailable"} {
			if !strings.Contains(prompt, want) {
				t.Errorf("missing %q", want)
			}
		}
	}
}

func TestDecisionsAreRenderedGlobalAndProject(t *testing.T) {
	t.Parallel()

	prompt := SystemPrompt(StartParams{Decisions: []*backendv1.ContextDecision{
		{Id: "g", Title: "[Core memory] Bind every repository", Content: "Register a known directory and bind it."},
		{Id: "p", Title: "Use worktrees", Content: "One worktree per change."},
	}}, nil, "")
	for _, want := range []string{
		"Active decisions",
		"- [Core memory] Bind every repository: Register a known directory and bind it.",
		"- Use worktrees: One worktree per change.",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt omits %q", want)
		}
	}
	if strings.Index(prompt, "Bind every repository") > strings.Index(prompt, "Use worktrees") {
		t.Error("global decisions must come before the project's")
	}
}

func TestNoDecisionAddsNothing(t *testing.T) {
	t.Parallel()

	for _, params := range []StartParams{{}, {Decisions: []*backendv1.ContextDecision{{}}}} {
		if prompt := SystemPrompt(params, nil, ""); strings.Contains(prompt, "Active decisions") {
			t.Error("an empty decision list produced a section")
		}
	}
}
