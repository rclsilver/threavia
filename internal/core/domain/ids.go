// Package domain holds the provider-independent Threavia domain model: the
// identifiers, enumerations and state transitions described in
// THREAVIA_SPEC_V1.md sections 3 to 8.
//
// This package must never import provider-specific (Claude, Codex, ...) or
// transport-specific concepts.
package domain

import "github.com/google/uuid"

// Identifier types. All identifiers are opaque strings backed by UUIDs; nothing
// outside this package may rely on their internal shape.
type (
	// UserID identifies the owner of Projects and BackendInstances. Its concrete
	// value depends on the configured authentication mode (spec section 20).
	UserID string

	// ProjectID identifies a Project.
	ProjectID string

	// SessionID identifies a Session, the long-lived user-visible work context.
	SessionID string

	// RunID identifies a Run, the binding between a Session and one
	// BackendInstance/provider native session.
	RunID string

	// JobID identifies a Job, one unit of agent work inside a Run.
	JobID string

	// EventID identifies a persistent Event.
	EventID string

	// BackendInstanceID identifies a BackendInstance.
	BackendInstanceID string
)

// NewUUID returns a new random UUID string, the canonical form of every
// Threavia identifier.
func NewUUID() string { return uuid.NewString() }

// NewProjectID returns a fresh ProjectID.
func NewProjectID() ProjectID { return ProjectID(NewUUID()) }

// NewSessionID returns a fresh SessionID.
func NewSessionID() SessionID { return SessionID(NewUUID()) }

// NewRunID returns a fresh RunID.
func NewRunID() RunID { return RunID(NewUUID()) }

// NewJobID returns a fresh JobID.
func NewJobID() JobID { return JobID(NewUUID()) }

// NewEventID returns a fresh EventID.
func NewEventID() EventID { return EventID(NewUUID()) }

// NewBackendInstanceID returns a fresh BackendInstanceID.
func NewBackendInstanceID() BackendInstanceID { return BackendInstanceID(NewUUID()) }

// Sequence is the Core-assigned, monotonically increasing global event sequence.
// It is the cursor used by the global client SSE stream (spec section 4).
type Sequence int64
