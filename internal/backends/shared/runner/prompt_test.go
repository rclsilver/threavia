package runner

import (
	"strings"
	"testing"

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
