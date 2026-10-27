package runner

import (
	"context"
	"math"
	"strings"
	"time"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
)

// ProviderBlock is a confirmed provider refusal, never a tool's stderr or a
// guess from a quota percentage. RetryAt is supplied by the provider if known.
type ProviderBlock struct {
	Reason        string
	Message       string
	RetryAt       *time.Time
	ActiveSeconds uint32
	// InputReceipt is scoped to this Job and its native conversation, and is
	// set only after the provider echoed the initial user input.
	InputReceipt string
}

func ProviderAttemptSeconds(started time.Time) uint32 {
	return uint32(math.Min(math.MaxUint32, math.Max(1, math.Ceil(time.Since(started).Seconds()))))
}

// ProviderBlockFor recognizes explicit provider error codes and a deliberately
// small set of CLI diagnostics. A generic 403, network error or overload is
// not evidence of expired authentication or of a subscription quota.
func ProviderBlockFor(provider, code, message string) *ProviderBlock {
	code = strings.ToLower(code)
	reason := ""
	switch code {
	case "authentication_required", "authentication_failed", "authentication_error", "unauthorized", "invalid_api_key", "401":
		reason = "AUTHENTICATION_REQUIRED"
	case "usagelimitexceeded", "usage_limit_exceeded", "insufficient_quota", "quota_exceeded", "billing_error":
		reason = "QUOTA_EXCEEDED"
	case "ratelimitexceeded", "rate_limit", "rate_limit_error", "429":
		reason = "RATE_LIMITED"
	}
	if reason == "" {
		lower := strings.ToLower(message)
		if strings.TrimSpace(lower) == "unauthorized" || strings.HasPrefix(strings.TrimSpace(lower), "api error: 401") {
			reason = "AUTHENTICATION_REQUIRED"
		}
		for _, phrase := range []string{"401 unauthorized", "invalid api key", "not logged in", "please run /login", "please log in again", "authentication token has expired", "oauth token has expired"} {
			if strings.Contains(lower, phrase) {
				reason = "AUTHENTICATION_REQUIRED"
				break
			}
		}
		if reason == "" {
			for _, phrase := range []string{"you've hit your usage limit", "you've hit your limit", "you have hit your usage limit", "usage limit reached", "quota exceeded", "insufficient_quota"} {
				if strings.Contains(lower, phrase) {
					reason = "QUOTA_EXCEEDED"
					break
				}
			}
		}
		if reason == "" && (strings.HasPrefix(strings.TrimSpace(lower), "api error: 429") || strings.Contains(lower, "429 too many requests") || strings.Contains(lower, "rate limit reached") || strings.Contains(lower, "rate limit exceeded")) {
			reason = "RATE_LIMITED"
		}
	}
	if reason == "" {
		return nil
	}
	text := "The provider quota is exhausted. Check the account limits on the backend machine; this Job will retry without changing its model."
	if reason == "RATE_LIMITED" {
		text = "The provider is rate limiting requests. This Job is waiting before retrying."
	}
	if reason == "AUTHENTICATION_REQUIRED" {
		command := "codex login"
		if provider == "claude" {
			command = "claude auth login"
		}
		text = "Provider authentication is required. Run " + command + " on the backend machine, or fix its configured API credential."
	}
	return &ProviderBlock{Reason: reason, Message: text}
}

// ReportProviderBlock lets older/custom sinks retain their terminal behavior.
func ReportProviderBlock(ctx context.Context, sink Sink, runID, jobID string, block *ProviderBlock, usage *backendv1.Usage) error {
	if reporter, ok := sink.(interface {
		ProviderBlocked(context.Context, string, string, *ProviderBlock, *backendv1.Usage) error
	}); ok {
		return reporter.ProviderBlocked(ctx, runID, jobID, block, usage)
	}
	return sink.JobFailed(ctx, runID, jobID, block.Reason, block.Message, usage)
}

// AuthReader performs a CLI-only check; it must never start an inference turn
// or return credentials. known=false means a failed or unsupported probe.
type AuthReader interface {
	Authenticated(context.Context) (authenticated, known bool)
}

// ProviderRetryAt reads only explicit provider timing fields.
func ProviderRetryAt(fields map[string]any, now time.Time) *time.Time {
	for _, key := range []string{"retryAt", "retry_at", "resetsAt", "resets_at", "resetAt", "reset_at"} {
		var at time.Time
		switch v := fields[key].(type) {
		case float64:
			at = time.Unix(int64(v), 0).UTC()
		case string:
			at, _ = time.Parse(time.RFC3339, v)
		}
		if at.After(now) {
			return &at
		}
	}
	for _, key := range []string{"retryAfterSeconds", "retry_after_seconds", "retry_after"} {
		if seconds, ok := fields[key].(float64); ok && seconds > 0 && seconds <= 366*24*60*60 {
			at := now.Add(time.Duration(seconds * float64(time.Second)))
			return &at
		}
	}
	return nil
}
