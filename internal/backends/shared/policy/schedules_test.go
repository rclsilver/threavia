package policy

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
)

func TestNativeSchedulingAlwaysUsesThreavia(t *testing.T) {
	for _, mode := range []backendv1.ExecutionMode{backendv1.ExecutionMode_EXECUTION_MODE_INTERACTIVE, backendv1.ExecutionMode_EXECUTION_MODE_GUARDED, backendv1.ExecutionMode_EXECUTION_MODE_AUTONOMOUS, backendv1.ExecutionMode_EXECUTION_MODE_SUPERVISED} {
		p := Policy{Mode: mode, Rules: []Rule{{Effect: backendv1.PermissionEffect_PERMISSION_EFFECT_ALLOW, Capability: backendv1.PermissionCapability_PERMISSION_CAPABILITY_TOOL, Match: "*"}}}
		settings, err := p.Native().Settings()
		if err != nil {
			t.Fatal(err)
		}
		var document struct {
			Permissions Native `json:"permissions"`
		}
		if err := json.Unmarshal([]byte(settings), &document); err != nil {
			t.Fatal(err)
		}
		for _, name := range nativeScheduleTools {
			if !slices.Contains(document.Permissions.Deny, name) {
				t.Errorf("%s: missing native denial %s", mode, name)
			}
			decision := p.Evaluate(name, nil)
			if decision.Verdict != Deny || !strings.Contains(decision.Reason, "mcp__threavia__schedule_") {
				t.Errorf("%s: %s = %+v", mode, name, decision)
			}
			if decision := p.EvaluateInvocation(name, nil, ""); decision.Verdict != Deny {
				t.Errorf("invocation gate allowed %s: %+v", name, decision)
			}
		}
		if d := p.Evaluate("mcp__threavia__schedule_create", nil); d.Verdict == Deny {
			t.Errorf("Threavia tool refused: %+v", d)
		}
	}
}
