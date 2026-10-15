package runner

import "testing"

func TestNativeResumeFailureIsConservative(t *testing.T) {
	for _, tt := range []struct{ provider, diagnostic, reason string }{
		{"claude", "No conversation found with session ID: abc", "NOT_FOUND"},
		{"codex", "no rollout found for thread id abc", "NOT_FOUND"},
		{"codex", "thread has been purged", "PURGED"},
		{"claude", "incompatible session format", "INCOMPATIBLE"},
		{"codex", "connection reset by peer", ""},
		{"codex", "context deadline exceeded", ""},
		{"claude", "401 Unauthorized: session not found", ""},
		{"claude", "unknown provider failure", ""},
		{"codex", "thread not found: permission denied reading rollout", ""},
		{"other", "session not found", ""},
	} {
		t.Run(tt.provider+"/"+tt.diagnostic, func(t *testing.T) {
			if got := NativeResumeFailure(tt.provider, tt.diagnostic); got != tt.reason {
				t.Fatalf("got %q, want %q", got, tt.reason)
			}
		})
	}
}
