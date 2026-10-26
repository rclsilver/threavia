package adapter

import (
	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/backends/shared/policy"
	"testing"
)

func TestMonitorChecksToolAndCommandRefusals(t *testing.T) {
	for _, name := range []string{"monitor_start", "mcp__threavia__monitor_start"} {
		p := policy.Policy{Mode: backendv1.ExecutionMode_EXECUTION_MODE_AUTONOMOUS, AllowFilesystemWrite: true, AllowNetwork: true, AllowGitCommit: true, AllowGitPush: true}
		input := map[string]any{"command": "echo event"}
		if err := monitorPolicyError(p, input, "."); err != nil {
			t.Fatal(err)
		}
		p.Rules = []policy.Rule{{Capability: backendv1.PermissionCapability_PERMISSION_CAPABILITY_TOOL, Effect: backendv1.PermissionEffect_PERMISSION_EFFECT_DENY, Match: name}}
		if err := monitorPolicyError(p, input, "."); err == nil {
			t.Fatalf("tool refusal %s was bypassed", name)
		}
		p.Rules = []policy.Rule{{Capability: backendv1.PermissionCapability_PERMISSION_CAPABILITY_SHELL, Effect: backendv1.PermissionEffect_PERMISSION_EFFECT_DENY, Match: "echo *"}}
		if err := monitorPolicyError(p, input, "."); err == nil {
			t.Fatal("shell refusal was bypassed")
		}
	}
}
