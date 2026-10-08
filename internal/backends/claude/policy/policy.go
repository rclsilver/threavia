// Package policy turns the ExecutionPolicy of THREAVIA_SPEC_V1.md section 17
// into something the provider enforces, and answers what the provider could not
// decide on its own.
//
// The division of labour is the point. Core states a policy in a vocabulary no
// provider owns — capabilities and rules — and this package renders it as
// Claude Code permission rules (see native.go). Claude Code then decides
// everything it can: it ships a built-in set of read-only commands and a
// shell-aware matcher that reads quoting, compound commands, substitutions and
// wrappers. It calls the permission tool only for what is left, and that is
// where Evaluate below answers.
//
// Classifying a shell command used to happen here, and it should not have: the
// list was ours to maintain and the parsing was ours to get wrong, which it was
// — a session spent approving `git grep "a\|b" -- src | head` is what retired
// it. What stays here is the half with teeth: the refusals, read by parsing the
// command rather than matching its text, because the provider's own
// documentation says a `Bash(git push *)` rule does not stop `git -C . push`.
//
// What this is not: a sandbox. It reads a command as written, so an agent
// determined to evade it could. Threavia's model has never been otherwise:
// filesystem and process access are the real permissions of the account the
// backend runs as, and section 28 says in as many words not to mistake Threavia
// metadata for a security boundary. This is a guard rail against an agent doing
// what it was told not to, and it should be read as exactly that.
package policy

import (
	"os"
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

// Rule is one permission rule as it arrived from Core.
type Rule struct {
	Effect     backendv1.PermissionEffect
	Capability backendv1.PermissionCapability
	Match      string
	Note       string
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
	// Rules arrive already merged from the Project, the Session and the Job.
	Rules []Rule
	// Scratch is where this Run writes the files it will read back. It is not
	// part of the policy Core states — it is this machine's answer to where
	// such a file belongs, and the gate needs it to say so when it refuses the
	// shared directory.
	Scratch string
}

// From converts the wire policy, falling back to the most restrained behaviour
// when a Job arrives without one: an absent policy must never be a permissive
// one.
func From(p *backendv1.ExecutionPolicy) Policy {
	if p == nil {
		return Policy{Mode: backendv1.ExecutionMode_EXECUTION_MODE_INTERACTIVE}
	}
	policy := Policy{
		Mode:                 p.GetMode(),
		AllowFilesystemWrite: p.GetAllowFilesystemWrite(),
		AllowGitCommit:       p.GetAllowGitCommit(),
		AllowGitPush:         p.GetAllowGitPush(),
		AllowNetwork:         p.GetAllowNetwork(),
		MaxDurationSeconds:   int(p.GetMaxDurationSeconds()),
		MaxActions:           int(p.GetMaxActions()),
	}
	for _, rule := range p.GetRules() {
		policy.Rules = append(policy.Rules, Rule{
			Effect:     rule.GetEffect(),
			Capability: rule.GetCapability(),
			Match:      rule.GetMatch(),
			Note:       rule.GetNote(),
		})
	}
	return policy
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
//
// It is reached only for what the provider did not already settle from the
// rules this policy gave it, so there is no classifying left to do here: either
// the action is refused, or it is the kind of thing this mode asks about.
func (p Policy) Evaluate(tool string, input map[string]any) Decision {
	switch {
	case writeTools[tool]:
		if !p.AllowFilesystemWrite {
			return Decision{Deny, "The execution policy of this session forbids writing to the filesystem."}
		}
	case networkTools[tool]:
		if !p.AllowNetwork {
			return Decision{Deny, "The execution policy of this session forbids network access."}
		}
	case tool == "Bash":
		if refusal, refused := p.refuse(command(input)); refused {
			return refusal
		}
	}

	if denial, refused := p.refusedByRule(tool, input); refused {
		return denial
	}

	// Writing where it will have to ask permission to read back.
	//
	// The shared temporary directory is outside every working directory, so a
	// file put there costs an approval to read — the agent asking a person for
	// the file it wrote a second earlier. It is told where to write instead,
	// and it was told; being told is not what changes a habit formed over a
	// long session, as the PATH prefix above already demonstrated.
	//
	// Only writing is refused. Reading something that is already there is
	// someone else's file and a legitimate thing to want.
	if p.Scratch != "" {
		if path := writesToSharedTemp(tool, input); path != "" {
			return Decision{Deny, "Writing to " + path + " means asking a person for permission to " +
				"read it back, since the shared temporary directory is outside every working " +
				"directory. Write it in " + p.Scratch + " instead: it belongs to this session, " +
				"it is readable without asking, and it outlives the message you are answering."}
		}
	}

	// A prefix that buys nothing and costs a person.
	//
	// `export PATH=/run/current-system/sw/bin:$PATH && grep -n foo src` cannot
	// be approved by any rule: the provider refuses to decide a command whose
	// value it cannot resolve before running it — "a variable in this command
	// can't be checked before it runs" — and that verdict stands over an
	// explicit allow. So one habitual prefix turns every reading command into a
	// question for someone who may be asleep, which is precisely what GUARDED
	// exists to avoid.
	//
	// Refusing it is the only answer that reaches the agent in time. It is told
	// why and what to do instead, and it retries in seconds; asking the user
	// would cost a round trip to a human for a command that was never in doubt.
	// Last, so that a genuine refusal above keeps its own reason.
	if tool == "Bash" && p.Mode != backendv1.ExecutionMode_EXECUTION_MODE_AUTONOMOUS {
		if assigned := specialAssignment(command(input)); assigned != "" {
			// No list of tools here. The system prompt names what is on PATH,
			// read from PATH rather than written down, and a second list kept
			// by hand is one that goes stale the day someone adds a package —
			// telling the agent a tool is absent when it is there.
			return Decision{Deny, "This command assigns " + assigned + ", which makes it impossible to " +
				"approve without asking a person: the value cannot be checked before it runs. " +
				"The tools you need are already on your PATH, and the system prompt lists them. " +
				"Run the command again without the assignment. If something is genuinely " +
				"missing, say so."}
		}
	}

	// AUTONOMOUS is the mode that does not ask. It is not permission to do
	// anything: the refusals above still stand, and the limits in the policy
	// are what make the mode safe to offer at all.
	if p.Mode == backendv1.ExecutionMode_EXECUTION_MODE_AUTONOMOUS {
		return Decision{Verdict: Allow}
	}
	return Decision{Verdict: Ask}
}

// refuse applies the capability switches to a shell command.
//
// This is the belt to the provider rules' braces: a rule matches the text of a
// command, and `git -C /srv/app push` is a push however it is written. Reading
// the arguments is what catches it.
func (p Policy) refuse(cmd string) (Decision, bool) {
	if cmd == "" {
		return Decision{}, false
	}
	for _, part := range splitCommand(cmd) {
		git := gitSubcommand(part)

		if !p.AllowGitPush && git == "push" {
			return Decision{Deny, "The execution policy of this session forbids pushing to a remote."}, true
		}
		if !p.AllowGitCommit && (git == "commit" || git == "am") {
			return Decision{Deny, "The execution policy of this session forbids committing."}, true
		}
		if !p.AllowNetwork && (networkCommand.MatchString(byName(part)) || remoteGitSubcommands[git]) {
			return Decision{Deny, "The execution policy of this session forbids network access."}, true
		}
	}
	return Decision{}, false
}

// refusedByRule applies the DENY rules a second time, here rather than only in
// the provider.
//
// Only the refusals are repeated. An ALLOW or an ASK that the provider did not
// apply costs a prompt, which is a nuisance; a DENY it did not apply costs the
// thing the rule existed to prevent.
func (p Policy) refusedByRule(tool string, input map[string]any) (Decision, bool) {
	for _, rule := range p.Rules {
		if rule.Effect != backendv1.PermissionEffect_PERMISSION_EFFECT_DENY {
			continue
		}
		if !rule.covers(tool, input) {
			continue
		}
		reason := "The execution policy of this session refuses this."
		if rule.Note != "" {
			reason = "The execution policy of this session refuses this: " + rule.Note
		}
		return Decision{Deny, reason}, true
	}
	return Decision{}, false
}

// covers reports whether a rule is about this invocation.
func (r Rule) covers(tool string, input map[string]any) bool {
	switch r.Capability {
	case backendv1.PermissionCapability_PERMISSION_CAPABILITY_SHELL:
		if tool != "Bash" {
			return false
		}
		cmd := command(input)
		if r.Match == "" {
			return true
		}
		// Every part of a compound command, so a refusal cannot hide behind a
		// harmless prefix — the same reading the switches above get.
		for _, part := range splitCommand(cmd) {
			if matches(r.Match, byName(part)) {
				return true
			}
		}
		return false

	case backendv1.PermissionCapability_PERMISSION_CAPABILITY_GIT_PUSH,
		backendv1.PermissionCapability_PERMISSION_CAPABILITY_GIT_COMMIT:
		if tool != "Bash" {
			return false
		}
		want := "push"
		if r.Capability == backendv1.PermissionCapability_PERMISSION_CAPABILITY_GIT_COMMIT {
			want = "commit"
		}
		for _, part := range splitCommand(command(input)) {
			sub := gitSubcommand(part)
			if sub == want || (want == "commit" && sub == "am") {
				return true
			}
		}
		return false

	case backendv1.PermissionCapability_PERMISSION_CAPABILITY_FILE_WRITE:
		return writeTools[tool]

	case backendv1.PermissionCapability_PERMISSION_CAPABILITY_NETWORK:
		if networkTools[tool] {
			return true
		}
		if tool != "Bash" {
			return false
		}
		for _, part := range splitCommand(command(input)) {
			if networkCommand.MatchString(byName(part)) {
				return true
			}
		}
		return false

	case backendv1.PermissionCapability_PERMISSION_CAPABILITY_TOOL:
		return tool == r.Match

	default:
		// FILE_READ is left to the provider: it resolves paths against the
		// working directory, which is where that question belongs.
		return false
	}
}

// Redirections and the one command that writes a file as an argument. A
// quoted mention is not a write, which is why this reads the scanned parts
// rather than the raw command.
var redirectToTemp = regexp.MustCompile(`>>?\s*["']?(` + regexp.QuoteMeta(os.TempDir()) + `/[^\s"';|&]+)`)
var teeToTemp = regexp.MustCompile(`\btee\b[^|;&]*?["']?(` + regexp.QuoteMeta(os.TempDir()) + `/[^\s"';|&]+)`)

// writesToSharedTemp returns the path a tool call would write into the shared
// temporary directory, or an empty string when it writes nowhere near it.
func writesToSharedTemp(tool string, input map[string]any) string {
	temp := os.TempDir() + "/"

	if writeTools[tool] {
		path, _ := input["file_path"].(string)
		if strings.HasPrefix(path, temp) {
			return path
		}
		return ""
	}
	if tool != "Bash" {
		return ""
	}
	for _, part := range splitCommand(command(input)) {
		if found := redirectToTemp.FindStringSubmatch(part); found != nil {
			return found[1]
		}
		if found := teeToTemp.FindStringSubmatch(part); found != nil {
			return found[1]
		}
	}
	return ""
}

// Shell variables whose assignment the provider will not approve, because
// their value decides what the rest of the command resolves to.
var specialVariables = map[string]bool{
	"PATH": true, "IFS": true, "ENV": true, "BASH_ENV": true,
	"LD_PRELOAD": true, "LD_LIBRARY_PATH": true,
}

// specialAssignment returns the special variable a command assigns, if any.
func specialAssignment(cmd string) string {
	for _, part := range splitCommand(cmd) {
		fields := strings.Fields(part)
		if len(fields) > 0 && fields[0] == "export" {
			fields = fields[1:]
		}
		for _, field := range fields {
			name, _, assigns := strings.Cut(field, "=")
			if !assigns {
				// Past the leading assignments, the rest is the command.
				break
			}
			if specialVariables[name] {
				return name
			}
		}
	}
	return ""
}

// byName reduces the program of a command to the name it is known by, so that
// a rule written about `kubectl` is about every way of saying kubectl.
//
// A path defeats a rule otherwise, and the provider says as much: its own
// documentation lists `/usr/bin/curl https://example.com` among what a
// `Bash(curl *)` rule does not stop. A refusal that a prefix walks around is
// not a refusal, and the agent reaches for that prefix on its own — not to
// evade anything, but because it is unsure the program is on PATH.
//
// Only the rules read commands this way. What the provider decides for itself
// it decides for itself, and widening its built-in set is not this package's
// business.
func byName(part string) string {
	fields := strings.Fields(part)
	for i, field := range fields {
		// Leading NAME=value assignments precede the program, and their value
		// is a path often enough that mistaking one for the program is easy:
		// `KUBECONFIG=/tmp/k kubectl delete …` is a kubectl.
		if isAssignment(field) {
			continue
		}
		// The assignments are dropped rather than kept, which is also how the
		// provider reads a refusal: `Bash(rm *)` in deny matches
		// `FOO=bar rm -rf tmp/`.
		rest := append([]string{}, fields[i:]...)
		if slash := strings.LastIndexByte(rest[0], '/'); slash >= 0 {
			rest[0] = rest[0][slash+1:]
		}
		return strings.Join(rest, " ")
	}
	return part
}

// isAssignment reports whether a shell word is a NAME=value assignment.
func isAssignment(word string) bool {
	name, _, found := strings.Cut(word, "=")
	if !found || name == "" {
		return false
	}
	for i, r := range name {
		if r != '_' && !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(i > 0 && r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

// matches reports whether a pattern with `*` wildcards covers a string. It is
// the one wildcard every provider agrees on, so it is the one Core promises.
func matches(pattern, value string) bool {
	segments := strings.Split(pattern, "*")
	if len(segments) == 1 {
		return pattern == value
	}

	if !strings.HasPrefix(value, segments[0]) {
		return false
	}
	value = value[len(segments[0]):]

	last := segments[len(segments)-1]
	for _, segment := range segments[1 : len(segments)-1] {
		index := strings.Index(value, segment)
		if index < 0 {
			return false
		}
		value = value[index+len(segment):]
	}
	return strings.HasSuffix(value, last)
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
// push, and `git log --grep=push` is not. It is the whole reason this survives
// the move of classification into the provider, whose own documentation lists
// `git -C . push origin main` among what its rules do not stop.
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

var networkCommand = regexp.MustCompile(`(^|\s)(curl|wget|nc|ncat|telnet|ssh|scp|rsync|ftp)(\s|$)`)

// splitCommand breaks a command on the separators that start a new one, so a
// forbidden call cannot hide behind a harmless prefix.
func splitCommand(cmd string) []string {
	parts, _ := scanCommand(cmd)
	return parts
}

// Where a scan currently stands with respect to shell quoting.
const (
	bare = iota
	singleQuoted
	doubleQuoted
)

// scanCommand reads a command once, tracking quoting, and returns the commands
// it is made of along with whether anything outside quotes writes a file or
// runs a command of its own.
//
// Quoting has to be read or the refusals read the wrong thing: `grep "git push"
// release.md` is a search, and splitting on every separator regardless of
// quotes turns an alternation like `"a\|b"` into a pipeline of programs nobody
// has ever heard of.
//
// Single quotes make everything literal. Double quotes keep substitutions alive
// but take the meaning out of separators and redirections, which is exactly the
// distinction the shell itself draws.
func scanCommand(cmd string) (parts []string, escapes bool) {
	var current strings.Builder
	flush := func() {
		if trimmed := strings.TrimSpace(current.String()); trimmed != "" {
			parts = append(parts, trimmed)
		}
		current.Reset()
	}

	state := bare
	runes := []rune(cmd)
	for i := 0; i < len(runes); i++ {
		r := runes[i]

		switch state {
		case singleQuoted:
			// Not even a backslash means anything in here.
			if r == '\'' {
				state = bare
			}

		case doubleQuoted:
			switch {
			case r == '\\' && i+1 < len(runes):
				current.WriteRune(r)
				i++
				r = runes[i]
			case r == '"':
				state = bare
			case r == '`', r == '$' && i+1 < len(runes) && runes[i+1] == '(':
				escapes = true
			}

		default:
			switch {
			case r == '\\' && i+1 < len(runes):
				current.WriteRune(r)
				i++
				r = runes[i]
			case r == '\'':
				state = singleQuoted
			case r == '"':
				state = doubleQuoted
			case r == '`', r == '$' && i+1 < len(runes) && runes[i+1] == '(':
				escapes = true
			case r == '>', r == '<':
				escapes = true
			case r == ';', r == '|', r == '&', r == '\n':
				flush()
				continue
			}
		}
		current.WriteRune(r)
	}
	flush()
	return parts, escapes
}

// command reads the shell command out of a Bash tool input.
func command(input map[string]any) string {
	value, _ := input["command"].(string)
	return value
}
