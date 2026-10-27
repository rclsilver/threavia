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
		if err := monitorPolicyError(p, input, ".", true); err != nil {
			t.Fatal(err)
		}
		p.Rules = []policy.Rule{{Capability: backendv1.PermissionCapability_PERMISSION_CAPABILITY_TOOL, Effect: backendv1.PermissionEffect_PERMISSION_EFFECT_DENY, Match: name}}
		if err := monitorPolicyError(p, input, ".", true); err == nil {
			t.Fatalf("tool refusal %s was bypassed", name)
		}
		p.Rules = []policy.Rule{{Capability: backendv1.PermissionCapability_PERMISSION_CAPABILITY_SHELL, Effect: backendv1.PermissionEffect_PERMISSION_EFFECT_DENY, Match: "echo *"}}
		if err := monitorPolicyError(p, input, ".", true); err == nil {
			t.Fatal("shell refusal was bypassed")
		}
	}
}

// A sandbox is what makes a read-only or offline policy hold for a monitor.
// Without one the watch must be refused before anyone is asked to approve it,
// not run with limits nothing enforces.
func TestUnsandboxedMonitorRefusesLimitsNothingWouldEnforce(t *testing.T) {
	input := map[string]any{"command": "echo event"}
	permissive := policy.Policy{Mode: backendv1.ExecutionMode_EXECUTION_MODE_AUTONOMOUS, AllowFilesystemWrite: true, AllowNetwork: true}
	if err := monitorPolicyError(permissive, input, ".", false); err != nil {
		t.Fatalf("a policy that limits nothing the sandbox would must still allow monitors: %v", err)
	}
	for _, p := range []policy.Policy{
		{Mode: permissive.Mode, AllowFilesystemWrite: false, AllowNetwork: true},
		{Mode: permissive.Mode, AllowFilesystemWrite: true, AllowNetwork: false},
	} {
		if err := monitorPolicyError(p, input, ".", false); err == nil {
			t.Fatalf("writes=%v network=%v accepted without a sandbox", p.AllowFilesystemWrite, p.AllowNetwork)
		}
		if err := monitorPolicyError(p, input, ".", true); err != nil {
			t.Fatalf("a sandboxed monitor enforces writes=%v network=%v itself: %v", p.AllowFilesystemWrite, p.AllowNetwork, err)
		}
	}
}
