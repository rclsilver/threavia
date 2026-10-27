package worker

import (
	"context"
	"errors"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/backends/shared/runner"
	"github.com/rclsilver/threavia/internal/backends/shared/workspace"
	"github.com/rclsilver/threavia/internal/execution/bridge"
	"github.com/rclsilver/threavia/internal/execution/protocol"
	"github.com/rclsilver/threavia/pkg/backend-sdk/quotas"
)

type remoteSink struct {
	peer         *bridge.Peer
	runID, jobID string
}

func (s *remoteSink) post(ctx context.Context, run, job string, event protocol.Event) error {
	if run != s.runID || job != s.jobID {
		return errors.New("cross-job event refused")
	}
	return s.peer.Call(ctx, "event", event, nil)
}
func (s *remoteSink) JobStarted(ctx context.Context, run, job string) error {
	return s.post(ctx, run, job, protocol.Event{Kind: "started"})
}
func (s *remoteSink) JobStartedWithModel(ctx context.Context, run, job, model, effort string) error {
	return s.post(ctx, run, job, protocol.Event{Kind: "started", Model: model, Effort: effort})
}
func (s *remoteSink) NativeSessionBound(ctx context.Context, run, job, native string) error {
	return s.post(ctx, run, job, protocol.Event{Kind: "native", NativeSessionID: native})
}
func (s *remoteSink) AgentMessage(ctx context.Context, run, job, text string) error {
	return s.post(ctx, run, job, protocol.Event{Kind: "message", Text: text})
}
func (s *remoteSink) ToolStarted(ctx context.Context, run, job, id, name string, input map[string]any) error {
	return s.post(ctx, run, job, protocol.Event{Kind: "tool_started", CallID: id, Name: name, Input: input})
}
func (s *remoteSink) ToolCompleted(ctx context.Context, run, job, id, name string, output map[string]any) error {
	return s.post(ctx, run, job, protocol.Event{Kind: "tool_completed", CallID: id, Name: name, Output: output})
}
func (s *remoteSink) ToolFailed(ctx context.Context, run, job, id, name, message string) error {
	return s.post(ctx, run, job, protocol.Event{Kind: "tool_failed", CallID: id, Name: name, Text: message})
}
func (s *remoteSink) WorkspaceChanged(ctx context.Context, run, job string, summary workspace.Summary) error {
	return s.post(ctx, run, job, protocol.Event{Kind: "workspace", Workspace: summary})
}
func (s *remoteSink) JobCompleted(ctx context.Context, run, job, summary string, usage *backendv1.Usage) error {
	return s.post(ctx, run, job, protocol.Event{Kind: "completed", Text: summary, Usage: usage})
}
func (s *remoteSink) JobFailed(ctx context.Context, run, job, code, message string, usage *backendv1.Usage) error {
	return s.post(ctx, run, job, protocol.Event{Kind: "failed", Code: code, Text: message, Usage: usage})
}
func (s *remoteSink) JobCancelled(ctx context.Context, run, job string) error {
	return s.post(ctx, run, job, protocol.Event{Kind: "cancelled"})
}
func (s *remoteSink) ProviderBlocked(ctx context.Context, run, job string, block *runner.ProviderBlock, usage *backendv1.Usage) error {
	return s.post(ctx, run, job, protocol.Event{Kind: "blocked", Block: block, Usage: usage})
}
func (s *remoteSink) Progress(ctx context.Context, run, job, kind, detail string) {
	_ = s.post(ctx, run, job, protocol.Event{Kind: "progress", Name: kind, Text: detail})
}
func (s *remoteSink) ReportQuotas(ctx context.Context, status quotas.Status, complete bool) error {
	return s.post(ctx, s.runID, s.jobID, protocol.Event{Kind: "quotas", Quotas: status, Complete: complete})
}
