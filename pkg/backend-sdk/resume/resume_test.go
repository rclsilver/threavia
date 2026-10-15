package resume

import "testing"

func TestFailureReasonsRemainOpenAndBounded(t *testing.T) {
	for _, reason := range []string{"NOT_FOUND", "PURGED", "INCOMPATIBLE", "FUTURE_REASON_2"} {
		if got, ok := FailureReason(FailureCode(reason)); !ok || got != reason {
			t.Fatal(got, ok)
		}
	}
	for _, code := range []string{"PROVIDER_ERROR", "AUTHENTICATION_REQUIRED", FailureCode(""), FailureCode("bad reason"), FailureCode("session-secret")} {
		if _, ok := FailureReason(code); ok {
			t.Fatal("accepted an unstructured failure reason", code)
		}
	}
}
