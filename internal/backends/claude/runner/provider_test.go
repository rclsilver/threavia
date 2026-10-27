package runner_test

import (
	"context"
	"strings"
	"testing"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/backends/claude/runner"
	"github.com/rclsilver/threavia/internal/backends/shared/policy"
	shared "github.com/rclsilver/threavia/internal/backends/shared/runner"
	"github.com/rclsilver/threavia/pkg/backend-sdk/resume"
)

type blockingSink struct {
	recordingSink
	block *shared.ProviderBlock
}

func TestClaudeQuotaReceiptRequiresEchoAndResumableBinding(t *testing.T) {
	for _, tc := range []struct {
		name                              string
		resumed, echo, assistant, receipt bool
	}{
		{"before-input", true, false, false, false},
		{"after-input", true, true, false, true},
		{"new-unbound", false, true, false, false},
		{"new-bound", false, true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script := ""
			if tc.echo {
				script += "printf '%s\\n' \"$first\" | sed 's/^{/{\"isReplay\":true,/'\n"
			}
			if tc.assistant {
				script += "echo '{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"Working\"}]}}'\n"
			}
			script += "echo '{\"type\":\"rate_limit_event\",\"rate_limit_info\":{\"status\":\"rejected\"}}'\nsleep 30"
			binary, _ := fakeClaude(t, script)
			p := runner.StartParams{RunID: "run", JobID: "job", WorkingDirectory: t.TempDir(), Prompt: "continue"}
			p.Attachments = []*backendv1.PromptAttachment{{Filename: "screen.png", MimeType: "image/png", Content: []byte{0, 1, 2}}}
			if tc.resumed {
				p.NativeSessionID = "native-existing"
			}
			sink := &blockingSink{}
			if err := newRunner(t, binary, runner.Options{Scratch: t.TempDir()}).Run(context.Background(), p, sink); err != nil {
				t.Fatal(err)
			}
			if sink.block == nil {
				t.Fatalf("expected quota wait: %+v", sink)
			}
			want := ""
			if tc.receipt {
				native := p.NativeSessionID
				if native == "" {
					native = sink.native
				}
				want = resume.InputReceipt(p.JobID, native)
			}
			if sink.block.InputReceipt != want || (tc.receipt && want == "") {
				t.Fatalf("receipt=%q want=%q", sink.block.InputReceipt, want)
			}
		})
	}
}

func (s *blockingSink) ProviderBlocked(_ context.Context, _, _ string, block *shared.ProviderBlock, usage *backendv1.Usage) error {
	s.block = block
	s.usage = usage
	s.record("job.waiting_backend")
	return nil
}

func TestClaudeProviderRefusalsStopAndWait(t *testing.T) {
	for _, tc := range []struct{ name, script, reason string }{
		{"auth", `echo '{"type":"assistant","error":"authentication_failed","message":{"content":[{"type":"text","text":"do not expose this credential diagnostic"}]}}'
echo '{"type":"result","is_error":true,"result":"provider refused","usage":{"input_tokens":7}}'`, "AUTHENTICATION_REQUIRED"},
		{"quota", `echo '{"type":"rate_limit_event","rate_limit_info":{"status":"rejected","resetsAt":4102444800}}'
sleep 30`, "QUOTA_EXCEEDED"},
		{"rate", `echo '{"type":"assistant","error":"rate_limit"}'
echo '{"type":"result","is_error":true,"result":"refused"}'`, "RATE_LIMITED"},
		{"stderr", `echo 'Invalid API key · Please run /login' >&2
exit 1`, "AUTHENTICATION_REQUIRED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			binary, _ := fakeClaude(t, tc.script)
			provider := newRunner(t, binary)
			sink := &blockingSink{}
			if err := provider.Run(context.Background(), runner.StartParams{RunID: "run", JobID: "job", WorkingDirectory: t.TempDir(), Prompt: "continue"}, sink); err != nil {
				t.Fatal(err)
			}
			if sink.block == nil || sink.block.Reason != tc.reason || sink.failure != "" || sink.native != "" || sink.block.ActiveSeconds == 0 {
				t.Fatalf("not a stopped provider wait: %+v", sink)
			}
			if len(sink.messages) > 0 {
				t.Fatal("provider auth errors became agent messages")
			}
			if tc.name == "auth" && (sink.usage == nil || sink.usage.InputTokens != 7) {
				t.Fatal("lost attempt usage")
			}
			if tc.name == "quota" && sink.block.RetryAt == nil {
				t.Fatal("lost provider reset")
			}
		})
	}
}

func TestClaudeRecoveryCannotResetDurationBudget(t *testing.T) {
	binary, _ := fakeClaude(t, `echo '{"type":"result","is_error":false,"result":"unexpected"}'`)
	sink := &recordingSink{}
	if err := newRunner(t, binary).Run(context.Background(), runner.StartParams{RunID: "run", JobID: "job", WorkingDirectory: t.TempDir(), Policy: policy.Policy{MaxDurationSeconds: 2}, ActiveSecondsUsed: 2}, sink); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(sink.failure, "POLICY_LIMIT:") || sink.native != "" {
		t.Fatal(sink)
	}
}

func TestClaudeRecoveryCannotResetActionBudget(t *testing.T) {
	binary, _ := fakeClaude(t, `echo '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"tool","name":"Read","input":{}}]}}'`)
	sink := &recordingSink{}
	if err := newRunner(t, binary).Run(context.Background(), runner.StartParams{RunID: "run", JobID: "job", WorkingDirectory: t.TempDir(), Policy: policy.Policy{MaxActions: 1}, ActionsUsed: 1}, sink); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(sink.failure, "POLICY_LIMIT:") {
		t.Fatal(sink)
	}
}
