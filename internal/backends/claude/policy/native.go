package policy

import (
	"encoding/json"
	"fmt"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
)

// Native is the provider configuration a Threavia policy becomes.
//
// This is the contract of the whole package: Core says what is allowed in its
// own vocabulary, and the backend renders it in the provider's. Claude Code
// then decides what it can decide on its own — it ships a built-in set of
// read-only commands and a shell-aware matcher that reads quoting, compound
// commands and wrappers — and calls the permission tool only for what is left.
//
// That division is deliberate. Classifying a shell command is the provider's
// problem, and it is better placed to solve it: the same list maintained here
// was asking the user to approve `git grep "a\|b" | head`.
type Native struct {
	// Mode is what --permission-mode receives.
	Mode string `json:"-"`

	Allow []string `json:"allow,omitempty"`
	Ask   []string `json:"ask,omitempty"`
	Deny  []string `json:"deny,omitempty"`
}

// Settings renders the JSON that --settings takes.
func (n Native) Settings() (string, error) {
	document := map[string]any{"permissions": n}
	encoded, err := json.Marshal(document)
	if err != nil {
		return "", fmt.Errorf("render the provider settings: %w", err)
	}
	return string(encoded), nil
}

// Native translates the policy into provider configuration.
func (p Policy) Native() Native {
	native := Native{Mode: "default"}

	// The switches first, as refusals. Each is also enforced by the gate below,
	// because a rule here matches the text of a command and nothing more: the
	// provider's own documentation says `Bash(git push *)` does not stop
	// `git -C . push`, and that one is read properly in refuse().
	if !p.AllowGitPush {
		native.Deny = append(native.Deny, "Bash(git push *)")
	}
	if !p.AllowGitCommit {
		native.Deny = append(native.Deny, "Bash(git commit *)", "Bash(git am *)")
	}
	if !p.AllowNetwork {
		native.Deny = append(native.Deny, "WebFetch", "WebSearch",
			"Bash(curl *)", "Bash(wget *)", "Bash(nc *)", "Bash(ssh *)",
			"Bash(scp *)", "Bash(rsync *)", "Bash(ftp *)", "Bash(telnet *)")
	}
	if !p.AllowFilesystemWrite {
		native.Deny = append(native.Deny, "Write", "Edit", "NotebookEdit")
	}

	// INTERACTIVE is the mode that asks about everything, reading included. The
	// provider runs its read-only set without asking in every mode, so the only
	// way back to a prompt is to say so.
	if p.Mode == backendv1.ExecutionMode_EXECUTION_MODE_INTERACTIVE {
		native.Ask = append(native.Ask, "Bash", "Read", "Glob", "Grep")
	}

	for _, rule := range p.Rules {
		for _, pattern := range patterns(rule) {
			switch rule.Effect {
			case backendv1.PermissionEffect_PERMISSION_EFFECT_DENY:
				native.Deny = append(native.Deny, pattern)
			case backendv1.PermissionEffect_PERMISSION_EFFECT_ASK:
				native.Ask = append(native.Ask, pattern)
			case backendv1.PermissionEffect_PERMISSION_EFFECT_ALLOW:
				native.Allow = append(native.Allow, pattern)
			}
		}
	}
	return native
}

// patterns renders one Threavia rule as the provider rules that carry it.
//
// A capability can need more than one: the provider has no single name for
// writing a file, so FILE_WRITE becomes every tool that does.
func patterns(rule Rule) []string {
	match := rule.Match

	switch rule.Capability {
	case backendv1.PermissionCapability_PERMISSION_CAPABILITY_SHELL:
		return []string{specifier("Bash", match)}

	case backendv1.PermissionCapability_PERMISSION_CAPABILITY_FILE_READ:
		if match == "" {
			return []string{"Read", "Glob", "Grep"}
		}
		return []string{specifier("Read", match)}

	case backendv1.PermissionCapability_PERMISSION_CAPABILITY_FILE_WRITE:
		if match == "" {
			return []string{"Write", "Edit", "NotebookEdit"}
		}
		return []string{specifier("Write", match), specifier("Edit", match)}

	case backendv1.PermissionCapability_PERMISSION_CAPABILITY_NETWORK:
		if match == "" {
			return []string{"WebFetch", "WebSearch"}
		}
		// A host, which the provider expresses as a domain specifier.
		return []string{"WebFetch(domain:" + match + ")"}

	case backendv1.PermissionCapability_PERMISSION_CAPABILITY_GIT_COMMIT:
		return []string{"Bash(git commit *)", "Bash(git am *)"}

	case backendv1.PermissionCapability_PERMISSION_CAPABILITY_GIT_PUSH:
		return []string{"Bash(git push *)"}

	case backendv1.PermissionCapability_PERMISSION_CAPABILITY_TOOL:
		// The name travels as written: an MCP tool is already named the way the
		// provider names it, and inventing a translation would only break it.
		return []string{match}

	default:
		// A capability this backend does not know is not quietly dropped into
		// something close enough. Core sends what it knows; a newer Core
		// talking to an older backend has to find the rule missing rather than
		// misapplied.
		return nil
	}
}

func specifier(tool, match string) string {
	if match == "" {
		return tool
	}
	return tool + "(" + match + ")"
}
