package remote

import (
	"context"
	"encoding/json"
	"testing"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/backends/shared/runner"
	"github.com/rclsilver/threavia/internal/execution/protocol"
	"github.com/rclsilver/threavia/pkg/backend-sdk/resume"
)

type inputSink struct {
	runner.Sink
	receipt string
}

func (s *inputSink) ProviderBlocked(_ context.Context, _, _ string, block *runner.ProviderBlock, _ *backendv1.Usage) error {
	s.receipt = block.InputReceipt
	return nil
}

func TestWorkerInputReceiptSurvivesTransportAndDelivery(t *testing.T) {
	receipt := resume.InputReceipt("job", "native")
	raw, err := json.Marshal(protocol.Event{Kind: "blocked", Block: &runner.ProviderBlock{Reason: "QUOTA_EXCEEDED", InputReceipt: receipt}})
	if err != nil {
		t.Fatal(err)
	}
	var event protocol.Event
	if err := json.Unmarshal(raw, &event); err != nil {
		t.Fatal(err)
	}
	sink := &inputSink{}
	if err := deliver(context.Background(), runner.StartParams{RunID: "run", JobID: "job"}, sink, event); err != nil {
		t.Fatal(err)
	}
	if sink.receipt != receipt {
		t.Fatal("worker lost the input acknowledgement", sink.receipt)
	}
}
