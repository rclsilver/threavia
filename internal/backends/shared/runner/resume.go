package runner

import "strings"

// NativeResumeFailure recognizes explicit permanent provider diagnostics only.
// Call exclusively for a failed resume handshake, before any agent work starts.
// Unknown diagnostics keep the binding: a timeout is not a lost conversation.
func NativeResumeFailure(provider, diagnostic string) string {
	message := strings.ToLower(strings.TrimSpace(diagnostic))
	if ProviderBlockFor(provider, "", message) != nil {
		return ""
	}
	for _, temporary := range []string{"permission denied", "access denied", "timed out", "timeout", "temporarily unavailable", "connection reset"} {
		if strings.Contains(message, temporary) {
			return ""
		}
	}
	var missing []string
	switch provider {
	case "claude":
		missing = []string{"no conversation found with session id", "session not found", "conversation not found"}
	case "codex":
		missing = []string{"thread not found", "conversation not found", "no rollout found for thread", "no rollout found for conversation", "rollout path does not exist"}
	default:
		return ""
	}
	for _, phrase := range missing {
		if strings.Contains(message, phrase) {
			return "NOT_FOUND"
		}
	}
	for phrase, reason := range map[string]string{
		"session has been purged": "PURGED", "conversation has been purged": "PURGED", "thread has been purged": "PURGED",
		"incompatible session format": "INCOMPATIBLE", "incompatible conversation format": "INCOMPATIBLE", "incompatible rollout format": "INCOMPATIBLE",
	} {
		if strings.Contains(message, phrase) {
			return reason
		}
	}
	return ""
}
