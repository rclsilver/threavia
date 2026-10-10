package policy

import (
	"net/url"
	"path/filepath"
	"strings"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
)

// EvaluateInvocation applies the complete rule contract when a provider calls
// the host before execution: capability switches, then DENY > ASK > ALLOW.
// Evaluate remains the fallback for Claude, whose native matcher settles ALLOW
// and ASK before reaching the host.
func (p Policy) EvaluateInvocation(tool string, input map[string]any, cwd string) Decision {
	baseline := p
	baseline.Rules = nil
	d := baseline.Evaluate(tool, input)
	if d.Verdict == Deny {
		return d
	}
	parts := []string{""}
	complex := false
	if tool == "Bash" {
		parts, complex = scanCommand(command(input))
		if len(parts) == 0 {
			return Decision{Deny, "An empty command cannot be approved."}
		}
	}
	allAllowed := true
	asks := false
	shellDir := invocationDirectory(input, cwd)
	for _, part := range parts {
		partInput := input
		if tool == "Bash" {
			partInput = make(map[string]any, len(input)+1)
			for k, v := range input {
				partInput[k] = v
			}
			partInput["cwd"] = shellDir
			delete(partInput, "workdir")
		}
		allowed := false
		for _, rule := range p.Rules {
			if !invocationMatch(rule, tool, partInput, cwd, part) {
				continue
			}
			switch rule.Effect {
			case backendv1.PermissionEffect_PERMISSION_EFFECT_DENY:
				return Decision{Deny, "The execution policy refuses this action. " + rule.Note}
			case backendv1.PermissionEffect_PERMISSION_EFFECT_ASK:
				asks = true
			case backendv1.PermissionEffect_PERMISSION_EFFECT_ALLOW:
				allowed = true
			}
		}
		allAllowed = allAllowed && allowed
		words := shellWords(part)
		if tool == "Bash" && len(words) == 2 && words[0] == "cd" {
			if filepath.IsAbs(words[1]) {
				shellDir = words[1]
			} else {
				shellDir = filepath.Join(shellDir, words[1])
			}
		}
	}
	if asks {
		return Decision{Verdict: Ask}
	}
	// A prefix allow must never cover a substitution, redirection, or another
	// command that has no allow of its own. Native approval still handles it.
	if allAllowed && !complex {
		return Decision{Verdict: Allow}
	}
	if tool == "Read" || tool == "Glob" || tool == "Grep" {
		if p.Mode != backendv1.ExecutionMode_EXECUTION_MODE_INTERACTIVE {
			return Decision{Verdict: Allow}
		}
	}
	return d
}

func invocationMatch(r Rule, tool string, input map[string]any, cwd, part string) bool {
	match := func(value string) bool { return r.Match == "" || matches(r.Match, value) }
	switch r.Capability {
	case backendv1.PermissionCapability_PERMISSION_CAPABILITY_SHELL:
		return tool == "Bash" && (match(byName(part)) || match(normalizedShell(part)))
	case backendv1.PermissionCapability_PERMISSION_CAPABILITY_TOOL:
		return match(tool)
	case backendv1.PermissionCapability_PERMISSION_CAPABILITY_FILE_READ,
		backendv1.PermissionCapability_PERMISSION_CAPABILITY_FILE_WRITE:
		if tool == "Bash" {
			paths, read, write := shellPaths(part)
			if r.Capability == backendv1.PermissionCapability_PERMISSION_CAPABILITY_FILE_READ && !read || r.Capability == backendv1.PermissionCapability_PERMISSION_CAPABILITY_FILE_WRITE && !write {
				return false
			}
			if r.Effect == backendv1.PermissionEffect_PERMISSION_EFFECT_ALLOW {
				// Only elementary file operations can inherit a FILE allow.
				// Executables, scripts and programmable utilities need SHELL.
				words := shellWords(part)
				name := filepath.Base(words[0])
				if r.Capability == backendv1.PermissionCapability_PERMISSION_CAPABILITY_FILE_READ {
					switch name {
					case "cat", "head", "tail", "wc", "stat":
					default:
						return false
					}
					if write {
						return false
					}
				} else {
					switch name {
					case "touch", "mkdir", "rmdir", "rm", "chmod", "chown":
					default:
						return false
					}
				}
				if len(paths) == 0 {
					return false
				}
				for _, path := range paths {
					if !filepath.IsAbs(path) {
						path = filepath.Join(invocationDirectory(input, cwd), path)
					}
					covered := false
					for _, candidate := range pathCandidates(path, cwd) {
						covered = covered || match(candidate)
					}
					if !covered {
						return false
					}
				}
				return true
			}
			for _, path := range paths {
				if !filepath.IsAbs(path) {
					path = filepath.Join(invocationDirectory(input, cwd), path)
				}
				for _, candidate := range pathCandidates(path, cwd) {
					if match(candidate) {
						return true
					}
					// A recursive listing/search of a parent also reads matching
					// descendants. Narrowing the root avoids the refusal.
					if read && r.Effect != backendv1.PermissionEffect_PERMISSION_EFFECT_ALLOW {
						if containsRulePath(candidate, r.Match) {
							return true
						}
					}
				}
			}
			return false
		}
		reading := tool == "Read" || tool == "Glob" || tool == "Grep"
		if r.Capability == backendv1.PermissionCapability_PERMISSION_CAPABILITY_FILE_READ && !reading ||
			r.Capability == backendv1.PermissionCapability_PERMISSION_CAPABILITY_FILE_WRITE && !writeTools[tool] {
			return false
		}
		path, _ := input["file_path"].(string)
		if path == "" {
			path, _ = input["path"].(string)
		}
		if path == "" {
			path = cwd
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(invocationDirectory(input, cwd), path)
		}
		for _, candidate := range pathCandidates(path, cwd) {
			if match(candidate) {
				return true
			}
			if (tool == "Glob" || tool == "Grep") && r.Effect != backendv1.PermissionEffect_PERMISSION_EFFECT_ALLOW && containsRulePath(candidate, r.Match) {
				return true
			}
		}
		return false
	case backendv1.PermissionCapability_PERMISSION_CAPABILITY_NETWORK:
		if networkTools[tool] {
			host, _ := input["host"].(string)
			if host == "" {
				host, _ = input["url"].(string)
			}
			if parsed, err := url.Parse(host); err == nil && parsed.Hostname() != "" {
				host = parsed.Hostname()
			}
			return match(host)
		}
		if tool != "Bash" || !(networkCommand.MatchString(byName(part)) || remoteGitSubcommands[gitSubcommand(part)]) {
			return false
		}
		if r.Match == "" {
			return true
		}
		for _, word := range shellWords(part) {
			if parsed, err := url.Parse(word); err == nil && parsed.Hostname() != "" && match(parsed.Hostname()) {
				return true
			}
			// SSH/scp's user@host:path form.
			if _, host, ok := strings.Cut(word, "@"); ok {
				host, _, _ = strings.Cut(host, ":")
				if match(host) {
					return true
				}
			}
			if match(word) {
				return true
			}
		}
		return false
	case backendv1.PermissionCapability_PERMISSION_CAPABILITY_GIT_COMMIT:
		return tool == "Bash" && (gitSubcommand(part) == "commit" || gitSubcommand(part) == "am")
	case backendv1.PermissionCapability_PERMISSION_CAPABILITY_GIT_PUSH:
		return tool == "Bash" && gitSubcommand(part) == "push"
	}
	return false
}

func invocationDirectory(input map[string]any, root string) string {
	for _, key := range []string{"workdir", "cwd"} {
		if dir, ok := input[key].(string); ok && dir != "" {
			if !filepath.IsAbs(dir) {
				return filepath.Join(root, dir)
			}
			return dir
		}
	}
	return root
}

func containsRulePath(directory, pattern string) bool {
	if directory == "." || directory == "./." {
		return !filepath.IsAbs(pattern)
	}
	return strings.HasPrefix(pattern, strings.TrimSuffix(directory, "/")+"/")
}

// Shell file rules are guard rails for named files and ordinary utilities, as
// native Claude Read rules are. Arbitrary scripts require their own SHELL rule;
// no textual matcher can establish every file an executable may open.
func shellPaths(part string) (paths []string, read, write bool) {
	words := shellWords(part)
	if len(words) == 0 {
		return
	}
	name := filepath.Base(words[0])
	switch name {
	case "cat", "head", "tail", "less", "more", "wc", "file", "stat", "ls", "find", "rg", "grep", "sed", "awk", "sort", "uniq", "cut", "diff", "git":
		read = true
	case "rm", "mkdir", "touch", "rmdir", "tee", "chmod", "chown":
		write = true
	case "cp", "mv":
		read, write = true, true
	default:
		// Conservatively inspect named paths even for executables whose file
		// access cannot be classified from their name.
		read, write = true, true
	}
	for _, word := range words[1:] {
		if strings.HasPrefix(word, "-") {
			if name == "sed" && strings.HasPrefix(word, "-i") {
				write = true
			}
			continue
		}
		paths = append(paths, word)
	}
	for i, word := range words {
		if word == ">" || word == "<" {
			if word == ">" {
				write = true
			} else {
				read = true
			}
			if i+1 < len(words) && words[i+1] != ">" {
				paths = append(paths, words[i+1])
			}
		}
	}
	if read && (name == "rg" || name == "grep" || name == "ls" || name == "find") && len(paths) <= 1 {
		paths = append(paths, ".")
	}
	return
}

func shellWords(text string) []string {
	var words []string
	var word strings.Builder
	quote := rune(0)
	escape := false
	flush := func() {
		if word.Len() > 0 {
			words = append(words, word.String())
			word.Reset()
		}
	}
	for _, ch := range text {
		if escape {
			word.WriteRune(ch)
			escape = false
			continue
		}
		if ch == '\\' && quote != '\'' {
			escape = true
			continue
		}
		if quote != 0 {
			if ch == quote {
				quote = 0
			} else {
				word.WriteRune(ch)
			}
			continue
		}
		if ch == '\'' || ch == '"' {
			quote = ch
			continue
		}
		if ch == ' ' || ch == '\t' {
			flush()
			continue
		}
		if ch == '>' || ch == '<' {
			flush()
			words = append(words, string(ch))
			continue
		}
		word.WriteRune(ch)
	}
	flush()
	for len(words) > 0 && isAssignment(words[0]) {
		words = words[1:]
	}
	return words
}

func normalizedShell(part string) string {
	words := shellWords(part)
	if len(words) == 0 {
		return part
	}
	words[0] = filepath.Base(words[0])
	return strings.Join(words, " ")
}

func pathCandidates(path, cwd string) []string {
	if !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
	}
	path = filepath.Clean(path)
	paths := []string{path}
	// Test the spelled path and its symlink target (or nearest existing parent
	// for a new file). An alias must not bypass a refusal on the real path.
	parent := path
	for {
		if real, err := filepath.EvalSymlinks(parent); err == nil {
			suffix, _ := filepath.Rel(parent, path)
			paths = append(paths, filepath.Join(real, suffix))
			break
		}
		next := filepath.Dir(parent)
		if next == parent {
			break
		}
		parent = next
	}
	roots := []string{cwd}
	if real, err := filepath.EvalSymlinks(cwd); err == nil {
		roots = append(roots, real)
	}
	for _, absolute := range append([]string{}, paths...) {
		for _, root := range roots {
			if rel, err := filepath.Rel(root, absolute); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				paths = append(paths, rel, "./"+rel)
			}
		}
	}
	return paths
}
