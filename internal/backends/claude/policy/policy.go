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

var networkCommand = regexp.MustCompile(`(^|\s)(curl|wget|nc|ncat|telnet|ssh|scp|rsync|ftp)(\s|$)`)

// Commands that only observe, whatever their arguments. Anything not listed is
// treated as mutating, because guessing the other way is how a guard rail stops
// being one.
var readOnlyPrograms = map[string]bool{
	"ls": true, "cat": true, "head": true, "tail": true, "grep": true, "egrep": true,
	"fgrep": true, "wc": true, "file": true, "stat": true, "pwd": true, "echo": true,
	"printf": true, "which": true, "date": true, "sort": true, "uniq": true,
	"cut": true, "tr": true, "diff": true, "tree": true, "du": true, "df": true,
	"id": true, "whoami": true, "uname": true, "realpath": true, "readlink": true,
	"dirname": true, "basename": true, "jq": true, "true": true, "test": true,
	// Searching a tree is the most common read there is, and both of these
	// already have an entry in escapingArgs below — which was unreachable while
	// they were missing here, so every ripgrep and every find went to the user.
	"rg": true, "find": true, "fd": true,
	"false": true, "nl": true, "comm": true, "paste": true, "seq": true,
	"column": true, "rev": true, "tac": true, "fold": true, "ps": true,
	"md5sum": true, "sha1sum": true, "sha256sum": true, "cksum": true,
	"xxd": true, "od": true, "strings": true,
}

// Parts that only shape the shell the rest of the command runs in. They change
// no file, so they neither make a command mutating nor read-only on their own.
var shellSetup = map[string]bool{"export": true, "cd": true, "set": true}

// git subcommands that only observe.
var readOnlyGit = map[string]bool{
	"status": true, "log": true, "diff": true, "show": true, "describe": true,
	"rev-parse": true, "ls-files": true, "ls-tree": true, "grep": true,
	"blame": true, "shortlog": true, "rev-list": true, "cat-file": true,
}

// git subcommands that list with no argument or only these flags, and change
// something otherwise (`git branch -D`, `git remote add`).
var listingGit = map[string]map[string]bool{
	"branch": {"-a": true, "-r": true, "-v": true, "-vv": true, "--list": true, "--show-current": true, "--all": true},
	"remote": {"-v": true},
	"tag":    {"-l": true, "--list": true},
}

// go subcommands that only observe. `go env -w` writes, and is refused by its
// argument.
var readOnlyGo = map[string]bool{"vet": true, "list": true, "version": true, "doc": true, "env": true}

// Arguments that turn an otherwise observing command into one that writes or
// runs something else.
var escapingArgs = map[string][]string{
	"find": {"-delete", "-exec", "-execdir", "-ok", "-okdir", "-fprint", "-fprint0", "-fprintf", "-fls"},
	"rg":   {"--pre"},
	"git":  {"-c", "--config-env", "--output", "-O", "--open-files-in-pager", "--ext-diff"},
	"go":   {"-w", "-toolexec", "-exec"},
	"sort": {"-o", "--output"},
	"tree": {"-o"},
}

// Redirections that write nowhere a user would care about. They are removed
// before a command is read, so `grep x 2>/dev/null` stays a read.
var harmlessRedirection = regexp.MustCompile(`\s*(2>&1|[12&]?>\s*/dev/null)`)

// readOnlyCommand reports whether every part of a command only observes.
func readOnlyCommand(cmd string) bool {
	// Substitutions run a command of their own wherever they appear, and a
	// redirection writes a file whatever the command in front of it is — but
	// only when the shell would read them as such, which is what the scan is
	// for.
	cleaned := harmlessRedirection.ReplaceAllString(cmd, "")
	parts, escapes := scanCommand(cleaned)
	if escapes {
		return false
	}

	reads := false
	for _, part := range parts {
		fields := strings.Fields(part)
		// Leading NAME=value assignments only set the environment of the command.
		for len(fields) > 0 && isAssignment(fields[0]) {
			fields = fields[1:]
		}
		if len(fields) == 0 || shellSetup[fields[0]] {
			continue
		}
		if !readOnlyPart(fields) {
			return false
		}
		reads = true
	}
	return reads
}

// readOnlyPart reports whether one command of a pipeline or a list only
// observes.
func readOnlyPart(fields []string) bool {
	program := fields[0][strings.LastIndexByte(fields[0], '/')+1:]
	for _, arg := range fields[1:] {
		for _, escaping := range escapingArgs[program] {
			if arg == escaping || strings.HasPrefix(arg, escaping+"=") {
				return false
			}
		}
	}

	switch program {
	case "env":
		// Alone it prints the environment; followed by a command it runs it.
		return len(fields) == 1
	case "sed":
		// `sed -n 40,60p file` is how a long file gets read a page at a time,
		// and it is constant in this work. What writes is -i, in any of the
		// spellings a short option takes: -i, -i.bak, -ni.
		//
		// A `w` or an `e` inside the script writes and runs too. They are not
		// read here, and that is the package's stated limit: this is a guard
		// rail against an agent doing what it was told not to, never a sandbox
		// against one trying to get around it.
		for _, arg := range fields[1:] {
			if arg == "--in-place" || strings.HasPrefix(arg, "--in-place=") {
				return false
			}
			if strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") &&
				strings.ContainsRune(arg, 'i') {
				return false
			}
		}
		return true
	case "git":
		return readOnlyGitPart(strings.Join(fields, " "))
	case "go":
		return len(fields) > 1 && readOnlyGo[fields[1]]
	default:
		return readOnlyPrograms[program]
	}
}

// readOnlyGitPart reports whether a git invocation only observes.
func readOnlyGitPart(part string) bool {
	sub := gitSubcommand(part)
	if readOnlyGit[sub] {
		return true
	}
	flags, listing := listingGit[sub]
	if !listing {
		return false
	}
	fields := strings.Fields(part)
	for i, field := range fields {
		if field != sub {
			continue
		}
		for _, arg := range fields[i+1:] {
			if !flags[arg] {
				return false
			}
		}
		return true
	}
	return false
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
// Quoting is the whole of it, and reading it was the difference between a guard
// rail and a nuisance. `git grep "a\|b" -- src | head` is a search and a head,
// not six programs: splitting on every separator regardless of quotes turned
// the alternation into a pipeline of programs nobody has ever heard of, and an
// unclassified program is treated as mutating. A plain search therefore went to
// the user for approval under GUARDED — the one mode whose entire purpose is to
// let reads through. The same blindness made `grep "a > b" f` look like a
// redirection.
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
