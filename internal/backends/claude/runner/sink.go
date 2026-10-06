package runner

import "context"

// Sink receives everything the provider produces, already normalised into the
// provider-independent vocabulary of the Threavia protocol.
//
// Keeping the translation here, behind an interface, is what stops Claude
// concepts from leaking into the SDK or into Core.
type Sink interface {
	// JobStarted reports that the provider process is running.
	JobStarted(ctx context.Context, runID, jobID string) error
	// NativeSessionBound reports the provider session backing the Run.
	NativeSessionBound(ctx context.Context, runID, jobID, nativeSessionID string) error

	// AgentMessage reports agent output.
	AgentMessage(ctx context.Context, runID, jobID, text string) error
	// ToolStarted reports a provider tool invocation.
	ToolStarted(ctx context.Context, runID, jobID, callID, name string, input map[string]any) error
	// ToolCompleted reports a successful tool invocation.
	ToolCompleted(ctx context.Context, runID, jobID, callID, name string, output map[string]any) error
	// ToolFailed reports a failed tool invocation.
	ToolFailed(ctx context.Context, runID, jobID, callID, name, message string) error

	// JobCompleted, JobFailed and JobCancelled are terminal: exactly one of them
	// is emitted per Job.
	JobCompleted(ctx context.Context, runID, jobID, summary string) error
	JobFailed(ctx context.Context, runID, jobID, code, message string) error
	JobCancelled(ctx context.Context, runID, jobID string) error

	// Progress streams a liveness-only signal. It is never persisted.
	Progress(ctx context.Context, runID, jobID, kind, detail string)
}
