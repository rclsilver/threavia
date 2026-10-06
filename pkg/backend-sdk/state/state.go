// Package state defines the durable local state every BackendInstance keeps.
//
// PostgreSQL remains the global platform truth; this local state is only
// execution and recovery truth (THREAVIA_SPEC_V1.md section 10). It lets a
// backend keep working and buffer unacknowledged events while Core is
// unreachable, and lets it reconcile after reconnecting.
//
// The storage technology is not part of the wire protocol contract: the
// specification suggests SQLite for the Go SDK, and MemoryStore below is the
// non-durable implementation used by tests and by the first vertical slice.
package state

import (
	"context"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
)

// Identity is the persistent BackendInstance identity obtained at registration.
// It survives reboots: registration credentials are not required at each start.
type Identity struct {
	BackendInstanceID string
	// Token is the persistent backend credential presented on every Connect.
	Token string
	// Name is the instance name advertised to Core.
	Name string
}

// RunRecord is a Run the backend knows about, with the provider native session
// bound to it.
type RunRecord struct {
	RunID           string
	NativeSessionID string
	ResumeStatus    backendv1.ResumeStatus
}

// JobRecord is the backend view of a Job and its event sequence.
type JobRecord struct {
	JobID  string
	RunID  string
	Status backendv1.JobStatus
	// LastSequence is the highest event sequence emitted for this Job.
	LastSequence uint64
	// AckedSequence is the highest sequence Core acknowledged persisting.
	AckedSequence uint64
}

// Store is the durable local state of a BackendInstance.
//
// Implementations must be safe for concurrent use.
type Store interface {
	// LoadIdentity returns the stored identity. The boolean reports whether one
	// exists yet.
	LoadIdentity(ctx context.Context) (Identity, bool, error)
	// SaveIdentity persists the identity obtained at registration.
	SaveIdentity(ctx context.Context, identity Identity) error

	// SaveRun records or updates a known Run and its native session.
	SaveRun(ctx context.Context, run RunRecord) error
	// Runs returns every known Run, for the reconciliation report sent on
	// reconnect.
	Runs(ctx context.Context) ([]RunRecord, error)

	// SaveJob records or updates a Job.
	SaveJob(ctx context.Context, job JobRecord) error
	// Jobs returns every known Job.
	Jobs(ctx context.Context) ([]JobRecord, error)

	// NextSequence allocates the next monotonic event sequence for a Job.
	NextSequence(ctx context.Context, jobID string) (uint64, error)

	// AppendPending durably records an event before it is sent, so it survives a
	// Core outage.
	AppendPending(ctx context.Context, event *backendv1.JobEvent) error
	// PendingEvents returns every unacknowledged event, ordered by Job and
	// sequence, for replay after a reconnection.
	PendingEvents(ctx context.Context) ([]*backendv1.JobEvent, error)
	// Ack drops the events of a Job up to and including sequence, once Core has
	// confirmed persisting them.
	Ack(ctx context.Context, jobID string, sequence uint64) error

	// Close releases the underlying resources.
	Close() error
}
