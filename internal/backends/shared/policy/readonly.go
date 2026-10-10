package policy

import (
	"path/filepath"
	"strings"
)

// ReadOnlyInvocation is the conservative fallback when local native allow
// rules could skip the provider's normal approval classifier. Ordinary jobs
// continue to use that classifier. Unknown or programmable commands ask.
func ReadOnlyInvocation(input map[string]any) bool {
	parts, complex := scanCommand(command(input))
	if complex || len(parts) == 0 {
		return false
	}
	for _, part := range parts {
		words := shellWords(part)
		if len(words) == 0 || isAssignment(words[0]) {
			return false
		}
		switch filepath.Base(words[0]) {
		case "pwd", "ls", "cat", "head", "tail", "wc", "stat", "grep", "rg", "git":
		default:
			return false
		}
		if filepath.Base(words[0]) == "git" {
			for _, word := range words[1:] {
				if word == "-c" || strings.HasPrefix(word, "--exec-path") || strings.HasPrefix(word, "--config-env") {
					return false
				}
			}
			switch gitSubcommand(part) {
			case "status", "diff", "log", "show", "grep", "ls-files", "rev-parse", "ls-tree":
			default:
				return false
			}
		}
		for _, word := range words[1:] {
			if strings.HasPrefix(word, "--pre") || strings.HasPrefix(word, "--output") || strings.HasPrefix(word, "--ext-diff") || strings.HasPrefix(word, "--textconv") || strings.HasPrefix(word, "--open") {
				return false
			}
		}
	}
	return true
}
