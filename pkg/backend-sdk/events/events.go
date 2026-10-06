// Package events builds the normalized Job events a backend sends to Core.
//
// Every event carries the two backend-side identifiers of specification
// section 4: a backend event id, unique per BackendInstance, used by Core for
// deduplication, and a per-Job monotonic sequence used for ordering and replay
// detection. Allocating the sequence through the durable local state is what
// makes at-least-once delivery safe across restarts.
package events

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/pkg/backend-sdk/state"
)

// Factory builds Job events against a backend durable state.
type Factory struct {
	store state.Store
	newID func() string
	now   func() time.Time
}

// Option customises a Factory.
type Option func(*Factory)

// WithIDFunc overrides the backend event id generator, for tests.
func WithIDFunc(fn func() string) Option {
	return func(f *Factory) { f.newID = fn }
}

// WithClock overrides the clock, for tests.
func WithClock(fn func() time.Time) Option {
	return func(f *Factory) { f.now = fn }
}

// NewFactory returns a Factory allocating sequences from store.
func NewFactory(store state.Store, opts ...Option) *Factory {
	f := &Factory{store: store, newID: uuid.NewString, now: time.Now}
	for _, opt := range opts {
		opt(f)
	}
	return f
}

// build stamps a new event with its identity, sequence and timestamp.
func (f *Factory) build(ctx context.Context, runID, jobID string, apply func(*backendv1.JobEvent)) (*backendv1.JobEvent, error) {
	if runID == "" || jobID == "" {
		return nil, fmt.Errorf("run id and job id are required to build an event")
	}
	sequence, err := f.store.NextSequence(ctx, jobID)
	if err != nil {
		return nil, fmt.Errorf("allocate event sequence: %w", err)
	}
	event := &backendv1.JobEvent{
		BackendEventId:  f.newID(),
		BackendSequence: sequence,
		RunId:           runID,
		JobId:           jobID,
		OccurredAt:      timestamppb.New(f.now()),
	}
	apply(event)
	return event, nil
}

// JobStarted reports that the agent began working on the Job.
func (f *Factory) JobStarted(ctx context.Context, runID, jobID string) (*backendv1.JobEvent, error) {
	return f.build(ctx, runID, jobID, func(e *backendv1.JobEvent) {
		e.Body = &backendv1.JobEvent_JobStarted{JobStarted: &backendv1.JobStarted{}}
	})
}

// NativeSessionBound reports the provider native session id backing the Run. The
// Run native session id stays unknown to Core until this event arrives.
func (f *Factory) NativeSessionBound(ctx context.Context, runID, jobID, nativeSessionID string) (*backendv1.JobEvent, error) {
	return f.build(ctx, runID, jobID, func(e *backendv1.JobEvent) {
		e.Body = &backendv1.JobEvent_NativeSessionBound{
			NativeSessionBound: &backendv1.NativeSessionBound{NativeSessionId: nativeSessionID},
		}
	})
}

// AgentMessage carries an agent message. Messages are events: there is no
// separate message history.
func (f *Factory) AgentMessage(ctx context.Context, runID, jobID, text string, artifactIDs ...string) (*backendv1.JobEvent, error) {
	return f.build(ctx, runID, jobID, func(e *backendv1.JobEvent) {
		e.Body = &backendv1.JobEvent_AgentMessage{
			AgentMessage: &backendv1.AgentMessage{Text: text, ArtifactIds: artifactIDs},
		}
	})
}

// ToolStarted reports a provider tool invocation.
func (f *Factory) ToolStarted(ctx context.Context, runID, jobID, toolCallID, name string, input *structpb.Struct) (*backendv1.JobEvent, error) {
	return f.build(ctx, runID, jobID, func(e *backendv1.JobEvent) {
		e.Body = &backendv1.JobEvent_ToolStarted{
			ToolStarted: &backendv1.ToolStarted{ToolCallId: toolCallID, Name: name, Input: input},
		}
	})
}

// ToolCompleted reports a successful provider tool invocation.
func (f *Factory) ToolCompleted(ctx context.Context, runID, jobID, toolCallID, name string, output *structpb.Struct) (*backendv1.JobEvent, error) {
	return f.build(ctx, runID, jobID, func(e *backendv1.JobEvent) {
		e.Body = &backendv1.JobEvent_ToolCompleted{
			ToolCompleted: &backendv1.ToolCompleted{ToolCallId: toolCallID, Name: name, Output: output},
		}
	})
}

// ToolFailed reports a failed provider tool invocation.
func (f *Factory) ToolFailed(ctx context.Context, runID, jobID, toolCallID, name, code, message string) (*backendv1.JobEvent, error) {
	return f.build(ctx, runID, jobID, func(e *backendv1.JobEvent) {
		e.Body = &backendv1.JobEvent_ToolFailed{
			ToolFailed: &backendv1.ToolFailed{
				ToolCallId: toolCallID,
				Name:       name,
				Error:      &backendv1.Error{Code: code, Message: message},
			},
		}
	})
}

// ValidationRequested asks for a permission or approval. Core turns it into a
// durable ValidationRequest and sets the Job to WAITING_VALIDATION; it never
// times out.
func (f *Factory) ValidationRequested(ctx context.Context, runID, jobID string, request *backendv1.ValidationRequested) (*backendv1.JobEvent, error) {
	return f.build(ctx, runID, jobID, func(e *backendv1.JobEvent) {
		e.Body = &backendv1.JobEvent_ValidationRequested{ValidationRequested: request}
	})
}

// UserInputRequested asks for an answer, a choice or information. It is distinct
// from a validation and sets the Job to WAITING_INPUT.
func (f *Factory) UserInputRequested(ctx context.Context, runID, jobID string, request *backendv1.UserInputRequested) (*backendv1.JobEvent, error) {
	return f.build(ctx, runID, jobID, func(e *backendv1.JobEvent) {
		e.Body = &backendv1.JobEvent_UserInputRequested{UserInputRequested: request}
	})
}

// WorkspaceChanged reports a structured filesystem change summary. Detailed
// diffs stay backend-owned and are fetched on demand.
func (f *Factory) WorkspaceChanged(ctx context.Context, runID, jobID string, change *backendv1.WorkspaceChanged) (*backendv1.JobEvent, error) {
	return f.build(ctx, runID, jobID, func(e *backendv1.JobEvent) {
		e.Body = &backendv1.JobEvent_WorkspaceChanged{WorkspaceChanged: change}
	})
}

// JobCompleted reports a successful Job.
func (f *Factory) JobCompleted(ctx context.Context, runID, jobID, summary string) (*backendv1.JobEvent, error) {
	return f.build(ctx, runID, jobID, func(e *backendv1.JobEvent) {
		e.Body = &backendv1.JobEvent_JobCompleted{JobCompleted: &backendv1.JobCompleted{Summary: summary}}
	})
}

// JobFailed reports a failed Job.
func (f *Factory) JobFailed(ctx context.Context, runID, jobID, code, message string) (*backendv1.JobEvent, error) {
	return f.build(ctx, runID, jobID, func(e *backendv1.JobEvent) {
		e.Body = &backendv1.JobEvent_JobFailed{
			JobFailed: &backendv1.JobFailed{Error: &backendv1.Error{Code: code, Message: message}},
		}
	})
}

// JobCancelled confirms that the backend actually stopped the work. Only then
// does Core move the Job from CANCELLING to CANCELLED.
func (f *Factory) JobCancelled(ctx context.Context, runID, jobID string) (*backendv1.JobEvent, error) {
	return f.build(ctx, runID, jobID, func(e *backendv1.JobEvent) {
		e.Body = &backendv1.JobEvent_JobCancelled{JobCancelled: &backendv1.JobCancelled{}}
	})
}

// Ephemeral builds a liveness-only event. Core may fan it out to connected
// clients but never persists it.
func (f *Factory) Ephemeral(runID, jobID, kind, detail string) *backendv1.EphemeralJobEvent {
	return &backendv1.EphemeralJobEvent{
		RunId:      runID,
		JobId:      jobID,
		Kind:       kind,
		Detail:     detail,
		OccurredAt: timestamppb.New(f.now()),
	}
}
