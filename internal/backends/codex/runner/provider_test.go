package runner

import (
	"context"
	"encoding/json"
	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	shared "github.com/rclsilver/threavia/internal/backends/shared/runner"
	"github.com/rclsilver/threavia/pkg/backend-sdk/resume"
	"testing"
)

type blockingSink struct {
	*recordingSink
	block *shared.ProviderBlock
}

func TestCodexQuotaReceiptRequiresObservedInputIncludingBeforeRPCResponse(t *testing.T) {
	for _, scenario := range []string{"provider-input-before-response", "provider-input-after-response", "provider-input-missing", "provider-input-without-image"} {
		t.Run(scenario, func(t *testing.T) {
			c, p, recorded := provider(t, scenario)
			p.Attachments = []*backendv1.PromptAttachment{{Filename: "screen.png", MimeType: "image/png", Content: []byte{0, 1, 2}}}
			sink := &blockingSink{recordingSink: recorded}
			if err := c.Run(context.Background(), p, sink); err != nil {
				t.Fatal(err)
			}
			if sink.block == nil {
				t.Fatalf("expected quota wait, got %s %s", sink.code, sink.summary)
			}
			want := resume.InputReceipt(p.JobID, "thread-1")
			if scenario == "provider-input-missing" || scenario == "provider-input-without-image" {
				want = ""
			}
			if sink.block.InputReceipt != want {
				t.Fatalf("receipt=%q, want %q", sink.block.InputReceipt, want)
			}
		})
	}
}

func TestProviderRPCErrorPreservesOptionalData(t *testing.T) {
	for _, data := range []string{`{"codexErrorInfo":"unauthorized"}`, `"optional diagnostic"`, `null`} {
		var failure rpcError
		if err := json.Unmarshal([]byte(`{"code":-32000,"message":"request refused","data":`+data+`}`), &failure); err != nil {
			t.Fatal(err)
		}
		if failure.Message != "request refused" {
			t.Fatal(failure)
		}
	}
}

func (s *blockingSink) ProviderBlocked(_ context.Context, _, _ string, block *shared.ProviderBlock, usage *backendv1.Usage) error {
	s.block = block
	s.usage = usage
	s.append("waiting")
	return nil
}

func TestCodexProviderRefusalsWaitWithoutHidingOtherErrors(t *testing.T) {
	for _, tc := range []struct{ scenario, reason string }{{"provider-auth", "AUTHENTICATION_REQUIRED"}, {"provider-quota", "QUOTA_EXCEEDED"}, {"provider-rate", "RATE_LIMITED"}, {"provider-generic", ""}} {
		t.Run(tc.scenario, func(t *testing.T) {
			provider, params, recording := provider(t, tc.scenario)
			sink := &blockingSink{recordingSink: recording}
			if err := provider.Run(context.Background(), params, sink); err != nil {
				t.Fatal(err)
			}
			if tc.reason == "" {
				if sink.block != nil || sink.code != "PROVIDER_ERROR" {
					t.Fatalf("unexpected provider result: block=%+v code=%q detail=%q", sink.block, sink.code, sink.summary)
				}
				return
			}
			if sink.block == nil || sink.block.Reason != tc.reason || sink.block.RetryAt == nil || sink.code != "" || sink.block.ActiveSeconds == 0 {
				t.Fatalf("bad provider wait: %+v", sink)
			}
		})
	}
}

func TestCodexHTTPStatusEvidence(t *testing.T) {
	for _, tc := range []struct {
		status float64
		want   string
	}{{401, "AUTHENTICATION_REQUIRED"}, {429, "RATE_LIMITED"}, {403, ""}, {500, ""}} {
		block := codexProviderBlock(map[string]any{"codexErrorInfo": map[string]any{"httpConnectionFailed": map[string]any{"httpStatusCode": tc.status}}})
		if tc.want == "" {
			if block != nil {
				t.Fatal(block)
			}
		} else if block == nil || block.Reason != tc.want {
			t.Fatal(block)
		}
	}
}

func TestCodexRecoveryCannotResetDurationBudget(t *testing.T) {
	c, p, sink := provider(t, "success")
	p.Policy.MaxDurationSeconds = 2
	p.ActiveSecondsUsed = 2
	if err := c.Run(context.Background(), p, sink); err != nil {
		t.Fatal(err)
	}
	if sink.code != "POLICY_LIMIT" || sink.native != "" {
		t.Fatal(sink)
	}
}

func TestCodexRecoveryCannotResetActionBudget(t *testing.T) {
	c, p, sink := provider(t, "success")
	p.Policy.MaxActions = 1
	p.ActionsUsed = 1
	if err := c.Run(context.Background(), p, sink); err != nil {
		t.Fatal(err)
	}
	if sink.code != "POLICY_LIMIT" {
		t.Fatal(sink)
	}
}
