// Package policy enforces the ExecutionPolicy of THREAVIA_SPEC_V1.md
// section 17 at the point where it can actually be enforced: the permission gate
// the provider calls before it acts.
//
// The specification is explicit that a policy is enforced rather than suggested
// — a policy forbidding a push rejects the push instead of asking the model not
// to. That is what this package does.
//
// What it is not: a sandbox. It reads a command as written, so an agent
// determined to evade it could. Threavia's model has never been otherwise:
// filesystem and process access are the real permissions of the account the
// backend runs as, and section 28 says in as many words not to mistake Threavia
// metadata for a security boundary. This is a guard rail against an agent doing
// what it was told not to, and it should be read as exactly that.
package policy

import (
	"regexp"
	"strings"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
)

// Verdict is what the gate decides about one tool invocation.
type Verdict int

const (
	// Ask puts the decision to the user.
	Ask Verdict = iota
	// Allow lets the action proceed without asking.
	Allow
	// Deny refuses it outright, without asking: the policy already answered.
	Deny
)

// Decision is a verdict and, when it refuses, why.
type Decision struct {
	Verdict Verdict
	// Reason is shown to the agent, so it understands the refusal is structural
	// rather than a user who happened to say no.
	Reason string
}

// Policy is the backend view of an ExecutionPolicy.
type Policy struct {
	Mode                 backendv1.ExecutionMode
	AllowFilesystemWrite bool
	AllowGitCommit       bool
	AllowGitPush         bool
	AllowNetwork         bool
	MaxDurationSeconds   int
	MaxActions           int
}

// From converts the wire policy, falling back to the most restrained behaviour
// when a Job arrives without one: an absent policy must never be a permissive
// one.
func From(p *backendv1.ExecutionPolicy) Policy {
	if p == nil {
		return Policy{Mode: backendv1.ExecutionMode_EXECUTION_MODE_INTERACTIVE}
	}
	return Policy{
		Mode:                 p.GetMode(),
		AllowFilesystemWrite: p.GetAllowFilesystemWrite(),
		AllowGitCommit:       p.GetAllowGitCommit(),
		AllowGitPush:         p.GetAllowGitPush(),
		AllowNetwork:         p.GetAllowNetwork(),
		MaxDurationSeconds:   int(p.GetMaxDurationSeconds()),
		MaxActions:           int(p.GetMaxActions()),
	}
}

// Tools that only read. Under GUARDED they proceed without asking, which is the
// whole difference between that mode and INTERACTIVE.
var readOnlyTools = map[string]bool{
	"Read": true, "Glob": true, "Grep": true, "NotebookRead": true,
	"TodoWrite": true, "ListMcpResources": true, "ReadMcpResource": true,
}

// Tools that write to the filesystem.
var writeTools = map[string]bool{
	"Write": true, "Edit": true, "MultiEdit": true, "NotebookEdit": true,
}

// Tools that reach the network.
var networkTools = map[string]bool{
	"WebFetch": true, "WebSearch": true,
}

// Evaluate decides what to do about one tool invocation.
func (p Policy) Evaluate(tool string, input map[string]any) Decision {
	switch {
	case writeTools[tool]:
		if !p.AllowFilesystemWrite {
			return Decision{Deny, "The execution policy of this session forbids writing to the filesystem."}
		}
		return p.mutating()

	case networkTools[tool]:
		if !p.AllowNetwork {
			return Decision{Deny, "The execution policy of this session forbids network access."}
		}
		return p.reading()

	case readOnlyTools[tool]:
		return p.reading()

	case tool == "Bash":
		return p.evaluateCommand(command(input))

	default:
		// An unknown tool is treated as mutating. A capability nobody classified
		// is not a capability to wave through.
		return p.mutating()
	}
}

// evaluateCommand applies the policy to a shell command.
func (p Policy) evaluateCommand(cmd string) Decision {
	if cmd == "" {
		return p.mutating()
	}
	for _, part := range splitCommand(cmd) {
		git := gitSubcommand(part)

		if !p.AllowGitPush && git == "push" {
			return Decision{Deny, "The execution policy of this session forbids pushing to a remote."}
		}
		if !p.AllowGitCommit && (git == "commit" || git == "am") {
			return Decision{Deny, "The execution policy of this session forbids committing."}
		}
		if !p.AllowNetwork && (networkCommand.MatchString(part) || remoteGitSubcommands[git]) {
			return Decision{Deny, "The execution policy of this session forbids network access."}
		}
	}

	if readOnlyCommand(cmd) {
		return p.reading()
	}
	return p.mutating()
}

// mutating is the verdict for an action that changes something.
func (p Policy) mutating() Decision {
	if p.Mode == backendv1.ExecutionMode_EXECUTION_MODE_AUTONOMOUS {
		return Decision{Verdict: Allow}
	}
	return Decision{Verdict: Ask}
}

// reading is the verdict for an action that only observes. GUARDED lets it
// through; INTERACTIVE still asks, which is what the mode is for.
func (p Policy) reading() Decision {
	switch p.Mode {
	case backendv1.ExecutionMode_EXECUTION_MODE_GUARDED,
		backendv1.ExecutionMode_EXECUTION_MODE_AUTONOMOUS:
		return Decision{Verdict: Allow}
	default:
		return Decision{Verdict: Ask}
	}
}

// git subcommands that talk to a remote.
var remoteGitSubcommands = map[string]bool{
	"clone": true, "fetch": true, "pull": true, "push": true, "remote": true, "ls-remote": true,
}

// git flags that take a separate value, which must not be mistaken for the
// subcommand.
var gitFlagsWithValue = map[string]bool{
	"-C": true, "-c": true, "--git-dir": true, "--work-tree": true,
	"--namespace": true, "--exec-path": true, "--config-env": true,
}

// gitSubcommand returns the subcommand of a git invocation, or an empty string
// when the part is not one.
//
// Reading the arguments beats matching a pattern: `git -C /srv/app push` is a
// push, and `git log --grep=push` is not.
func gitSubcommand(part string) string {
	fields := strings.Fields(part)

	i := 0
	for ; i < len(fields); i++ {
		if base := fields[i][strings.LastIndexByte(fields[i], '/')+1:]; base == "git" {
			break
		}
		// Leading NAME=value assignments precede the command itself.
		if !strings.Contains(fields[i], "=") {
			return ""
		}
	}
	if i >= len(fields) {
		return ""
	}

	for i++; i < len(fields); i++ {
		field := fields[i]
		if !strings.HasPrefix(field, "-") {
			return field
		}
		if gitFlagsWithValue[field] {
			i++
		}
	}
	return ""
}

var (
	networkCommand = regexp.MustCompile(`(^|\s)(curl|wget|nc|ncat|telnet|ssh|scp|rsync|ftp)(\s|$)`)
	// Commands that only observe. Anything not listed is treated as mutating,
	// because guessing the other way is how a guard rail stops being one.
	readOnly = regexp.MustCompile(`^\s*(ls|cat|head|tail|grep|rg|find|wc|file|stat|pwd|echo|which|env|date|git\s+(status|log|diff|show|branch|remote|describe|rev-parse|ls-files))(\s|$)`)
)

// readOnlyCommand reports whether every part of a command only observes.
func readOnlyCommand(cmd string) bool {
	parts := splitCommand(cmd)
	if len(parts) == 0 {
		return false
	}
	for _, part := range parts {
		if !readOnly.MatchString(part) {
			return false
		}
	}
	return true
}

// splitCommand breaks a command on the separators that start a new one, so a
// forbidden call cannot hide behind a harmless prefix.
func splitCommand(cmd string) []string {
	fields := strings.FieldsFunc(cmd, func(r rune) bool {
		return r == ';' || r == '|' || r == '&' || r == '\n'
	})
	parts := make([]string, 0, len(fields))
	for _, field := range fields {
		if trimmed := strings.TrimSpace(field); trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	return parts
}

// command reads the shell command out of a Bash tool input.
func command(input map[string]any) string {
	value, _ := input["command"].(string)
	return value
}
