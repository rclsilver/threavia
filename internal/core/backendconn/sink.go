package backendconn

import (
	"context"
	"io"
	"time"

	"google.golang.org/protobuf/types/known/structpb"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/core/domain"
)

// Sink receives everything a backend reports, so this package stays a pure
// transport: it owns the stream, the lease and the handshake, and knows nothing
// about Core state.
//
// Implementations must be safe for concurrent use: one goroutine per connected
// backend calls into them.
type Sink interface {
	// Connected is called after a successful handshake, with the lease id.
	Connected(ctx context.Context, instanceID domain.BackendInstanceID, connectionID string, hello *backendv1.Hello) error
	// Disconnected is called once the control stream is gone.
	Disconnected(ctx context.Context, instanceID domain.BackendInstanceID, connectionID string)

	// Heartbeat records backend liveness.
	Heartbeat(ctx context.Context, instanceID domain.BackendInstanceID, at time.Time, capacity *backendv1.Capacity)
	// StatusUpdate records a self-reported operational status.
	StatusUpdate(ctx context.Context, instanceID domain.BackendInstanceID, update *backendv1.StatusUpdate)
	QuotaReport(ctx context.Context, instanceID domain.BackendInstanceID, report *backendv1.QuotaReport)
	// ReconcileState receives what the backend believes is happening locally, so
	// Core can converge its desired state.
	ReconcileState(ctx context.Context, instanceID domain.BackendInstanceID, state *backendv1.ReconcileState)

	// JobEvent persists one observable event and returns the backend sequence
	// Core has now durably recorded for that Job, which is acknowledged back.
	JobEvent(ctx context.Context, instanceID domain.BackendInstanceID, event *backendv1.JobEvent) (uint64, error)
	// EphemeralJobEvent forwards a liveness-only signal. It is never persisted.
	EphemeralJobEvent(ctx context.Context, instanceID domain.BackendInstanceID, event *backendv1.EphemeralJobEvent)
	// CommandResult reports whether a dispatched command was accepted.
	CommandResult(ctx context.Context, instanceID domain.BackendInstanceID, result *backendv1.CommandResult)

	// CoreToolRequest executes a Core Tool on behalf of an agent.
	CoreToolRequest(ctx context.Context, instanceID domain.BackendInstanceID, request *backendv1.CoreToolRequest) (*structpb.Struct, error)

	// SkillInventory records the Skills a backend has locally. Core keeps the
	// metadata only; the content never leaves the backend (spec section 18).
	SkillInventory(ctx context.Context, instanceID domain.BackendInstanceID, inventory *backendv1.SkillInventory)
	// SkillBundle opens the packed bundle of a Core-managed Skill, so Core can
	// distribute it to a backend that does not have it cached.
	SkillBundle(ctx context.Context, instanceID domain.BackendInstanceID, skillID string) (io.ReadCloser, error)

	// WorkspaceDiff receives the answer to a diff Core asked a backend for.
	WorkspaceDiff(ctx context.Context, instanceID domain.BackendInstanceID, diff *backendv1.WorkspaceDiff)
	// RepositoryStatus receives the answer to a repository status Core asked a
	// backend for.
	RepositoryStatus(ctx context.Context, instanceID domain.BackendInstanceID, status *backendv1.RepositoryStatus)
}

// NopSink accepts everything and does nothing. It keeps the control service
// usable on its own, in tests and before the Core services are wired.
type NopSink struct{}

// Connected implements Sink.
func (NopSink) Connected(context.Context, domain.BackendInstanceID, string, *backendv1.Hello) error {
	return nil
}

// Disconnected implements Sink.
func (NopSink) Disconnected(context.Context, domain.BackendInstanceID, string) {}

// Heartbeat implements Sink.
func (NopSink) Heartbeat(context.Context, domain.BackendInstanceID, time.Time, *backendv1.Capacity) {}

// StatusUpdate implements Sink.
func (NopSink) StatusUpdate(context.Context, domain.BackendInstanceID, *backendv1.StatusUpdate) {}

func (NopSink) QuotaReport(context.Context, domain.BackendInstanceID, *backendv1.QuotaReport) {}

// ReconcileState implements Sink.
func (NopSink) ReconcileState(context.Context, domain.BackendInstanceID, *backendv1.ReconcileState) {}

// JobEvent implements Sink. Returning zero acknowledges nothing, so the backend
// keeps the event buffered.
func (NopSink) JobEvent(context.Context, domain.BackendInstanceID, *backendv1.JobEvent) (uint64, error) {
	return 0, nil
}

// EphemeralJobEvent implements Sink.
func (NopSink) EphemeralJobEvent(context.Context, domain.BackendInstanceID, *backendv1.EphemeralJobEvent) {
}

// CommandResult implements Sink.
func (NopSink) CommandResult(context.Context, domain.BackendInstanceID, *backendv1.CommandResult) {}

// CoreToolRequest implements Sink.
func (NopSink) CoreToolRequest(context.Context, domain.BackendInstanceID, *backendv1.CoreToolRequest) (*structpb.Struct, error) {
	return nil, ErrCoreToolUnavailable
}

// SkillInventory implements Sink.
func (NopSink) SkillInventory(context.Context, domain.BackendInstanceID, *backendv1.SkillInventory) {}

// SkillBundle implements Sink.
func (NopSink) SkillBundle(context.Context, domain.BackendInstanceID, string) (io.ReadCloser, error) {
	return nil, ErrSkillUnavailable
}

// WorkspaceDiff implements Sink.
func (NopSink) WorkspaceDiff(context.Context, domain.BackendInstanceID, *backendv1.WorkspaceDiff) {}

// RepositoryStatus implements Sink.
func (NopSink) RepositoryStatus(context.Context, domain.BackendInstanceID, *backendv1.RepositoryStatus) {
}
