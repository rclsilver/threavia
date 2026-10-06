package events

import (
	"encoding/json"
	"strconv"
	"time"

	"github.com/rclsilver/threavia/internal/core/domain"
)

// Envelope is the conceptual JSON representation of a persistent event exposed
// to clients (spec section 4). Scope ids are omitted when the event scope does
// not include that object.
type Envelope struct {
	ID       domain.EventID  `json:"id"`
	Sequence domain.Sequence `json:"sequence"`
	// Timestamp is when the event occurred, as observed by its producer.
	Timestamp time.Time `json:"timestamp"`
	Type      Type      `json:"type"`

	ProjectID *domain.ProjectID `json:"projectId,omitempty"`
	SessionID *domain.SessionID `json:"sessionId,omitempty"`
	RunID     *domain.RunID     `json:"runId,omitempty"`
	JobID     *domain.JobID     `json:"jobId,omitempty"`

	Payload json.RawMessage `json:"payload,omitempty"`
}

// BackendOrigin carries the backend-side identity of an event. It is used for
// deduplication and replay and is not exposed to clients (spec section 4):
//
//	BackendEventID  -> deduplication, unique per BackendInstance
//	BackendSequence -> ordering/replay within a Job
//	Envelope.Sequence (global) -> Core-wide client cursor
type BackendOrigin struct {
	BackendInstanceID domain.BackendInstanceID
	BackendEventID    string
	BackendSequence   uint64
}

// Record is one persisted event: the client-visible envelope plus its backend
// origin when it was produced by a BackendInstance.
type Record struct {
	Envelope
	Origin *BackendOrigin
}

// Cursor is a client position in the global event stream. A client reconnects
// with the last sequence it processed and catches up without maintaining a
// per-Session cursor.
type Cursor struct {
	After domain.Sequence `json:"after"`
}

// EventID is the SSE id of an event: the global sequence, so a reconnecting
// browser resumes exactly where it stopped. Ephemeral signals carry no sequence
// and therefore no id.
func (e Envelope) EventID() string {
	if e.Sequence == 0 {
		return ""
	}
	return strconv.FormatInt(int64(e.Sequence), 10)
}
