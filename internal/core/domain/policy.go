package domain

import (
	"fmt"
	"strings"
)

// ExecutionMode is how much the agent may do without asking (spec section 17).
type ExecutionMode string

const (
	// ExecutionInteractive asks the user before every action that is not purely
	// read-only.
	ExecutionInteractive ExecutionMode = "INTERACTIVE"
	// ExecutionGuarded asks only for actions that change something, and lets
	// reading proceed.
	ExecutionGuarded ExecutionMode = "GUARDED"
	// ExecutionAutonomous never asks. It is not permission to do anything: the
	// limits below still apply, and are what make the mode safe to offer.
	ExecutionAutonomous ExecutionMode = "AUTONOMOUS"
)

func (m ExecutionMode) String() string { return string(m) }

// Valid reports whether m is a known ExecutionMode.
func (m ExecutionMode) Valid() bool {
	switch m {
	case ExecutionInteractive, ExecutionGuarded, ExecutionAutonomous:
		return true
	default:
		return false
	}
}

// PermissionEffect is what a rule does to the action it matches.
type PermissionEffect string

const (
	// PermissionAllow lets the action run without asking.
	PermissionAllow PermissionEffect = "ALLOW"
	// PermissionAsk puts it to the user, whatever the mode would have done.
	PermissionAsk PermissionEffect = "ASK"
	// PermissionDeny refuses it outright. The user is not asked, because the
	// answer was given in advance.
	PermissionDeny PermissionEffect = "DENY"
)

// Valid reports whether e is a known effect.
func (e PermissionEffect) Valid() bool {
	switch e {
	case PermissionAllow, PermissionAsk, PermissionDeny:
		return true
	default:
		return false
	}
}

// PermissionCapability is what a rule is about, named independently of any
// provider.
//
// This vocabulary is the point of the whole thing. A Project says "never
// kubectl delete" once, and each backend translates that into whatever its own
// provider understands — Claude Code permission rules here, something else
// elsewhere. A rule written in a provider's own syntax would be a rule only one
// backend could honour, and the policy would stop being a contract.
type PermissionCapability string

const (
	// CapabilityShell is running a command. Match is a command pattern.
	CapabilityShell PermissionCapability = "SHELL"
	// CapabilityFileRead is reading a file. Match is a path pattern.
	CapabilityFileRead PermissionCapability = "FILE_READ"
	// CapabilityFileWrite is creating, editing or deleting one.
	CapabilityFileWrite PermissionCapability = "FILE_WRITE"
	// CapabilityNetwork is reaching the network. Match is a host pattern.
	CapabilityNetwork PermissionCapability = "NETWORK"
	// CapabilityGitCommit and CapabilityGitPush are named separately from the
	// shell because they are what people actually reason about, and because a
	// provider may expose them as something other than a command.
	CapabilityGitCommit PermissionCapability = "GIT_COMMIT"
	CapabilityGitPush   PermissionCapability = "GIT_PUSH"
	// CapabilityTool is a named provider tool, including an MCP one. Match is
	// its name, and is required: a rule about every tool at once is the mode's
	// job, not a rule's.
	CapabilityTool PermissionCapability = "TOOL"
)

// Valid reports whether c is a known capability.
func (c PermissionCapability) Valid() bool {
	switch c {
	case CapabilityShell, CapabilityFileRead, CapabilityFileWrite,
		CapabilityNetwork, CapabilityGitCommit, CapabilityGitPush, CapabilityTool:
		return true
	default:
		return false
	}
}

// PermissionRule is one exception to the switches, written once and understood
// by every backend.
type PermissionRule struct {
	Effect     PermissionEffect     `json:"effect"`
	Capability PermissionCapability `json:"capability"`
	// Match narrows the rule: a command for SHELL, a host for NETWORK, a path
	// for the file capabilities, a name for TOOL. An empty Match is the whole
	// capability. `*` stands for any text, which is the one wildcard every
	// provider agrees on.
	Match string `json:"match,omitempty"`
	// Note is why. It is kept for the audit and shown beside the rule, because
	// a rule nobody can explain is a rule nobody dares remove.
	Note string `json:"note,omitempty"`
}

// Validate checks one rule.
func (r PermissionRule) Validate() error {
	if !r.Effect.Valid() {
		return fmt.Errorf("unknown permission effect %q", r.Effect)
	}
	if !r.Capability.Valid() {
		return fmt.Errorf("unknown capability %q", r.Capability)
	}
	if r.Capability == CapabilityTool && strings.TrimSpace(r.Match) == "" {
		return fmt.Errorf("a TOOL rule has to name a tool")
	}
	if len(r.Match) > 512 || len(r.Note) > 512 {
		return fmt.Errorf("a permission rule is too long")
	}
	return nil
}

// Covers reports whether two rules are about the same thing, which is what
// decides whether one can override the other.
func (r PermissionRule) Covers(other PermissionRule) bool {
	return r.Capability == other.Capability && r.Match == other.Match
}

// ExecutionPolicy bounds what a Run may do.
//
// Section 17 is explicit that these are enforced rather than suggested: a policy
// forbidding a push rejects the push, instead of asking the model not to. The
// permission gate in the backend is where that happens, which is why the policy
// travels with every Job.
type ExecutionPolicy struct {
	Mode ExecutionMode `json:"mode"`

	AllowFilesystemWrite bool `json:"allowFilesystemWrite"`
	AllowGitCommit       bool `json:"allowGitCommit"`
	AllowGitPush         bool `json:"allowGitPush"`
	AllowNetwork         bool `json:"allowNetwork"`

	// Rules are the fine grain, evaluated before the switches above: deny
	// first, then ask, then allow, and the first match decides. They are what
	// lets a Project say "everything but kubectl delete" instead of choosing
	// between all of the shell and none of it.
	Rules []PermissionRule `json:"rules,omitempty"`

	// MaxDurationSeconds stops a Run that has been going too long. Zero means no
	// limit.
	MaxDurationSeconds int `json:"maxDurationSeconds,omitempty"`
	// MaxActions stops a Run that has taken too many steps. Zero means no limit.
	MaxActions int `json:"maxActions,omitempty"`
}

// DefaultExecutionPolicy is what a Session gets when nothing is configured: ask
// before acting, and refuse to push.
//
// The default is deliberately the most restrained of the three modes. A policy
// that has to be loosened on purpose is a policy someone thought about.
func DefaultExecutionPolicy() ExecutionPolicy {
	return ExecutionPolicy{
		Mode:                 ExecutionInteractive,
		AllowFilesystemWrite: true,
		AllowGitCommit:       true,
		AllowGitPush:         false,
		AllowNetwork:         true,
	}
}

// Validate checks a policy.
func (p ExecutionPolicy) Validate() error {
	if !p.Mode.Valid() {
		return fmt.Errorf("unknown execution mode %q", p.Mode)
	}
	if p.MaxDurationSeconds < 0 || p.MaxActions < 0 {
		return fmt.Errorf("execution limits cannot be negative")
	}
	if len(p.Rules) > 100 {
		return fmt.Errorf("a policy carries at most 100 permission rules")
	}
	for _, rule := range p.Rules {
		if err := rule.Validate(); err != nil {
			return err
		}
	}
	// An autonomous Run with no bound on duration or actions is a Run nobody can
	// stop, which is the one combination section 17 exists to prevent.
	if p.Mode == ExecutionAutonomous && p.MaxDurationSeconds == 0 && p.MaxActions == 0 {
		return fmt.Errorf("autonomous execution requires a duration or action limit")
	}
	return nil
}

// Binding returns the rules of a policy that no narrower level may lift: its
// refusals.
//
// A Project's switches are defaults — a Session that needs to push once says
// so, and that is the ordinary case. A Project's DENY is not a default. It is
// the sentence someone wrote down on purpose, and the reason to have a Project
// level at all.
func (p ExecutionPolicy) Binding() []PermissionRule {
	var binding []PermissionRule
	for _, rule := range p.Rules {
		if rule.Effect == PermissionDeny {
			binding = append(binding, rule)
		}
	}
	return binding
}

// Effective returns the policy that applies, from the Project default down to
// the Job override.
//
// The mode, the switches and the limits come from the narrowest level that sets
// one: a Session overrides its Project, a Job overrides its Session. The rules
// do not work that way. They are the union of every level, outermost first, and
// they are evaluated deny before ask before allow — so a refusal written on the
// Project stands however the Session is configured, because nothing below can
// take it out of the set. That is what makes a Project rule a limit rather than
// a suggestion.
func Effective(project, session, job *ExecutionPolicy) ExecutionPolicy {
	effective := DefaultExecutionPolicy()
	for _, level := range []*ExecutionPolicy{project, session, job} {
		if level != nil {
			effective = *level
		}
	}
	effective.Rules = MergeRules(project, session, job)
	return effective
}

// MergeRules gathers the rules of every level, outermost first.
//
// A rule that an outer level already refused in those exact terms is dropped
// rather than carried, so the merged set says what it means when it is read —
// but the guarantee does not rest on that. It rests on evaluation order: every
// DENY of every level is in the set, and deny is answered first.
func MergeRules(levels ...*ExecutionPolicy) []PermissionRule {
	var merged, binding []PermissionRule
	for _, level := range levels {
		if level == nil {
			continue
		}
		for _, rule := range level.Rules {
			if rule.Effect != PermissionDeny && covered(binding, rule) {
				continue
			}
			merged = append(merged, rule)
		}
		binding = append(binding, level.Binding()...)
	}
	return merged
}

// covered reports whether one of the binding rules is about the same thing.
func covered(binding []PermissionRule, rule PermissionRule) bool {
	for _, bound := range binding {
		if bound.Covers(rule) {
			return true
		}
	}
	return false
}
