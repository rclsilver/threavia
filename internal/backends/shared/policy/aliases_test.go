package policy

import (
	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"testing"
)

func TestToolAliasesMustComeFromHost(t *testing.T) {
	p := Policy{Mode: backendv1.ExecutionMode_EXECUTION_MODE_INTERACTIVE, AllowNetwork: true, Rules: []Rule{{Effect: backendv1.PermissionEffect_PERMISSION_EFFECT_ALLOW, Capability: backendv1.PermissionCapability_PERMISSION_CAPABILITY_TOOL, Match: "mcp__threavia__web_fetch"}}}
	input := map[string]any{"url": "https://example.com", "threavia_policy_alias": "mcp__threavia__web_fetch"}
	if p.EvaluateInvocation("WebFetch", input, "").Verdict != Ask {
		t.Fatal("model-controlled alias bypassed approval")
	}
	if p.EvaluateInvocationAliases("WebFetch", input, "", "mcp__threavia__web_fetch").Verdict != Allow {
		t.Fatal("host-selected alias was not recognized")
	}
	if p.Rules[0].Match != "mcp__threavia__web_fetch" {
		t.Fatal("evaluating an alias mutated the policy")
	}
}
