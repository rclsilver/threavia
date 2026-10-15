package runner_test

import (
	"context"
	"strings"
	"testing"

	"github.com/rclsilver/threavia/internal/backends/claude/runner"
	"github.com/rclsilver/threavia/pkg/backend-sdk/resume"
)

func TestNativeResumeFailuresOnlyInvalidateConfirmedLostConversations(t *testing.T) {
	for _, tt := range []struct {
		name, script   string
		resuming, lost bool
	}{
		{"missing", "echo 'No conversation found with session ID: old' >&2; exit 1", true, true},
		{"result-missing", "echo '{\"type\":\"result\",\"is_error\":true,\"result\":\"No conversation found with session ID: old\"}'", true, true},
		{"network", "echo 'connection reset by peer' >&2; exit 1", true, false},
		{"auth", "echo '401 Unauthorized: session not found' >&2; exit 1", true, false},
		{"fresh", "echo 'No conversation found with session ID: old' >&2; exit 1", false, false},
		{"after-init", "echo '{\"type\":\"system\",\"subtype\":\"init\",\"session_id\":\"old\"}'; echo '{\"type\":\"result\",\"is_error\":true,\"result\":\"session not found\"}'", true, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			binary, _ := fakeClaude(t, tt.script)
			params := runner.StartParams{RunID: "run", JobID: "job", WorkingDirectory: t.TempDir(), Prompt: "continue"}
			if tt.resuming {
				params.NativeSessionID = "old"
			}
			sink := &recordingSink{}
			if err := newRunner(t, binary).Run(context.Background(), params, sink); err != nil {
				t.Fatal(err)
			}
			lost := strings.HasPrefix(sink.failure, resume.FailureCode("NOT_FOUND"))
			if lost != tt.lost {
				t.Fatalf("lost=%v, want %v: %s", lost, tt.lost, sink.failure)
			}
		})
	}
}
