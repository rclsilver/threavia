// Package resume defines provider-independent permanent native-session failures.
package resume

import "strings"

const prefix = "NATIVE_SESSION_UNAVAILABLE:"
const attemptPrefix = "NATIVE_SESSION_RESUME_FAILED:"

// FailureCode is carried by JobFailed.error.code. Reasons are an open vocabulary.
// Only confirmed permanent resume failures may use it; never auth or transport errors.
func FailureCode(reason string) string { return prefix + reason }

func FailureReason(code string) (string, bool) {
	return parseReason(code, prefix)
}

// AttemptFailureCode retains the native binding: the failed handshake did not
// establish permanent loss. Authentication and quota use provider waiting.
func AttemptFailureCode(reason string) string { return attemptPrefix + reason }

func AttemptFailureReason(code string) (string, bool) { return parseReason(code, attemptPrefix) }

func parseReason(code, prefix string) (string, bool) {
	reason, ok := strings.CutPrefix(code, prefix)
	if !ok || len(reason) == 0 || len(reason) > 64 {
		return "", false
	}
	for _, c := range reason {
		if !(c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_') {
			return "", false
		}
	}
	return reason, true
}
