package policy

import backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"

// EvaluateInvocationAliases treats server-selected names as the same tool.
// Aliases never come from tool arguments or other model-controlled metadata.
func (p Policy) EvaluateInvocationAliases(tool string, input map[string]any, cwd string, aliases ...string) Decision {
	p.Rules = append([]Rule(nil), p.Rules...)
	for i, rule := range p.Rules {
		if rule.Capability != backendv1.PermissionCapability_PERMISSION_CAPABILITY_TOOL {
			continue
		}
		for _, alias := range aliases {
			if rule.Match == "" || matches(rule.Match, alias) {
				p.Rules[i].Match = tool
				break
			}
		}
	}
	return p.EvaluateInvocation(tool, input, cwd)
}
