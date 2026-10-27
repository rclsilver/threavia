package runner

import (
	"testing"
	"time"
)

func TestProviderFailuresRequireSpecificEvidence(t *testing.T) {
	for _, tc := range []struct{ code, message, want string }{
		{"authentication_failed", "details", "AUTHENTICATION_REQUIRED"},
		{"unauthorized", "details", "AUTHENTICATION_REQUIRED"},
		{"usageLimitExceeded", "details", "QUOTA_EXCEEDED"},
		{"usage_limit_exceeded", "details", "QUOTA_EXCEEDED"},
		{"rateLimitExceeded", "details", "RATE_LIMITED"},
		{"", "API Error: 401 unauthorized", "AUTHENTICATION_REQUIRED"},
		{"", "You've hit your limit · resets later", "QUOTA_EXCEEDED"},
		{"", "429 Too Many Requests", "RATE_LIMITED"},
		{"", "model unavailable", ""}, {"", "403 Forbidden", ""}, {"serverOverloaded", "try again", ""},
		{"contextWindowExceeded", "context too large", ""}, {"", "connection timed out", ""},
		{"", "Rate limit remaining: 0", ""}, {"", "A warning about authentication in the source file", ""},
	} {
		block := ProviderBlockFor("claude", tc.code, tc.message)
		if tc.want == "" {
			if block != nil {
				t.Fatalf("misclassified %q: %+v", tc.message, block)
			}
			continue
		}
		if block == nil || block.Reason != tc.want {
			t.Fatalf("%s %s: %+v", tc.code, tc.message, block)
		}
		if tc.want == "AUTHENTICATION_REQUIRED" && block.Message == tc.message {
			t.Fatal("leaked raw authentication diagnostic")
		}
	}
}

func TestProviderRetryTimingUsesOnlyExplicitFields(t *testing.T) {
	now := time.Now().UTC()
	future := now.Add(time.Hour)
	for _, fields := range []map[string]any{{"retry_at": future.Format(time.RFC3339)}, {"resetsAt": float64(future.Unix())}, {"retry_after": float64(60)}} {
		if at := ProviderRetryAt(fields, now); at == nil || !at.After(now) {
			t.Fatal(fields, at)
		}
	}
	if at := ProviderRetryAt(map[string]any{"percentUsed": float64(100), "resetsAt": float64(now.Add(-time.Hour).Unix())}, now); at != nil {
		t.Fatal(at)
	}
}
