package adapter

import (
	"context"
	"fmt"
	"log/slog"

	"google.golang.org/protobuf/types/known/structpb"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/backends/claude/runner"
	"github.com/rclsilver/threavia/pkg/backend-sdk/state"
)

// Adapter is the runner sink: it turns provider output into protocol events.
var _ runner.Sink = (*Adapter)(nil)

// send records an event durably and ships it to Core. Recording first is what
// makes a Core outage survivable.
func (a *Adapter) send(ctx context.Context, build func() (*backendv1.JobEvent, error)) error {
	sdk := a.sdk()
	if sdk == nil {
		return fmt.Errorf("the backend client is not bound yet")
	}
	event, err := build()
	if err != nil {
		return err
	}
	return sdk.SendEvent(ctx, event)
}

// JobStarted implements runner.Sink.
func (a *Adapter) JobStarted(ctx context.Context, runID, jobID string) error {
	return a.send(ctx, func() (*backendv1.JobEvent, error) {
		return a.sdk().Events().JobStarted(ctx, runID, jobID)
	})
}

// NativeSessionBound implements runner.Sink. It is also recorded locally, so a
// backend restart still knows which provider session backs the Run.
func (a *Adapter) NativeSessionBound(ctx context.Context, runID, jobID, nativeSessionID string) error {
	if err := a.store.SaveRun(ctx, state.RunRecord{
		RunID:           runID,
		NativeSessionID: nativeSessionID,
		ResumeStatus:    backendv1.ResumeStatus_RESUME_STATUS_AVAILABLE,
	}); err != nil {
		a.logger.Error("cannot record the native session locally", slog.String("error", err.Error()))
	}
	return a.send(ctx, func() (*backendv1.JobEvent, error) {
		return a.sdk().Events().NativeSessionBound(ctx, runID, jobID, nativeSessionID)
	})
}

// AgentMessage implements runner.Sink.
func (a *Adapter) AgentMessage(ctx context.Context, runID, jobID, text string) error {
	return a.send(ctx, func() (*backendv1.JobEvent, error) {
		return a.sdk().Events().AgentMessage(ctx, runID, jobID, text)
	})
}

// ToolStarted implements runner.Sink.
func (a *Adapter) ToolStarted(ctx context.Context, runID, jobID, callID, name string, input map[string]any) error {
	return a.send(ctx, func() (*backendv1.JobEvent, error) {
		return a.sdk().Events().ToolStarted(ctx, runID, jobID, callID, name, toStruct(input))
	})
}

// ToolCompleted implements runner.Sink.
func (a *Adapter) ToolCompleted(ctx context.Context, runID, jobID, callID, name string, output map[string]any) error {
	return a.send(ctx, func() (*backendv1.JobEvent, error) {
		return a.sdk().Events().ToolCompleted(ctx, runID, jobID, callID, name, toStruct(output))
	})
}

// ToolFailed implements runner.Sink.
func (a *Adapter) ToolFailed(ctx context.Context, runID, jobID, callID, name, message string) error {
	return a.send(ctx, func() (*backendv1.JobEvent, error) {
		return a.sdk().Events().ToolFailed(ctx, runID, jobID, callID, name, "TOOL_ERROR", message)
	})
}

// JobCompleted implements runner.Sink.
func (a *Adapter) JobCompleted(ctx context.Context, runID, jobID, summary string) error {
	a.recordTerminal(ctx, runID, jobID, backendv1.JobStatus_JOB_STATUS_COMPLETED)
	return a.send(ctx, func() (*backendv1.JobEvent, error) {
		return a.sdk().Events().JobCompleted(ctx, runID, jobID, summary)
	})
}

// JobFailed implements runner.Sink.
func (a *Adapter) JobFailed(ctx context.Context, runID, jobID, code, message string) error {
	a.recordTerminal(ctx, runID, jobID, backendv1.JobStatus_JOB_STATUS_FAILED)
	return a.send(ctx, func() (*backendv1.JobEvent, error) {
		return a.sdk().Events().JobFailed(ctx, runID, jobID, code, message)
	})
}

// JobCancelled implements runner.Sink. Core moves the Job to CANCELLED only
// when this arrives: the backend is the source of truth for what actually
// stopped.
func (a *Adapter) JobCancelled(ctx context.Context, runID, jobID string) error {
	a.recordTerminal(ctx, runID, jobID, backendv1.JobStatus_JOB_STATUS_CANCELLED)
	return a.send(ctx, func() (*backendv1.JobEvent, error) {
		return a.sdk().Events().JobCancelled(ctx, runID, jobID)
	})
}

// Progress implements runner.Sink. Ephemeral signals are dropped when Core is
// unreachable: they are liveness, never history.
func (a *Adapter) Progress(ctx context.Context, runID, jobID, kind, detail string) {
	sdk := a.sdk()
	if sdk == nil {
		return
	}
	sdk.SendEphemeral(ctx, sdk.Events().Ephemeral(runID, jobID, kind, detail))
}

// recordTerminal remembers how a Job ended, so a reconnection reports the truth
// rather than a Job that looks stuck.
func (a *Adapter) recordTerminal(ctx context.Context, runID, jobID string, status backendv1.JobStatus) {
	if err := a.store.SaveJob(ctx, state.JobRecord{JobID: jobID, RunID: runID, Status: status}); err != nil {
		a.logger.Error("cannot record the job outcome locally", slog.String("error", err.Error()))
	}
}

// toStruct converts a decoded JSON object for the wire, falling back to an empty
// one rather than failing an event over unrepresentable content.
func toStruct(value map[string]any) *structpb.Struct {
	if value == nil {
		return emptyStruct()
	}
	encoded, err := structpb.NewStruct(value)
	if err != nil {
		return emptyStruct()
	}
	return encoded
}
