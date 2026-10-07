package domain

import "time"

// Project is a logical body of work. It belongs to one user in V1 and groups
// Sessions (spec section 3.1).
type Project struct {
	ID          ProjectID `json:"id"`
	OwnerID     UserID    `json:"ownerId"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	// Instructions are the provider-independent project rules of specification
	// section 18. The backend maps them to whatever its provider reads.
	Instructions string        `json:"instructions,omitempty"`
	Status       ProjectStatus `json:"status"`
	CreatedAt    time.Time     `json:"createdAt"`
	UpdatedAt    time.Time     `json:"updatedAt"`
	ArchivedAt   *time.Time    `json:"archivedAt,omitempty"`
}

// Session is the long-lived user-visible work context. It is provider and
// backend independent and never holds a provider native session id
// (spec section 3.2).
type Session struct {
	ID        SessionID `json:"id"`
	ProjectID ProjectID `json:"projectId"`
	// Title is generated automatically, user-renamable and has no technical
	// meaning.
	Title  string        `json:"title"`
	Status SessionStatus `json:"status"`
	// WorkingDirectoryID is the optional logical working directory, preferably a
	// KnownDirectory. It is the initial/main cwd only, never a filesystem
	// boundary (spec sections 3.2 and 11).
	WorkingDirectoryID *string    `json:"workingDirectoryId,omitempty"`
	CreatedAt          time.Time  `json:"createdAt"`
	UpdatedAt          time.Time  `json:"updatedAt"`
	ArchivedAt         *time.Time `json:"archivedAt,omitempty"`
}

// Run binds a Session to one BackendInstance and one provider native session.
// Backend choice lives on the Run, not on the Session, and changing backend is
// explicit and creates a new Run (spec section 3.4).
type Run struct {
	ID                RunID             `json:"id"`
	SessionID         SessionID         `json:"sessionId"`
	BackendInstanceID BackendInstanceID `json:"backendInstanceId"`
	// NativeSessionID is nil until the backend reports it.
	NativeSessionID *string      `json:"nativeSessionId,omitempty"`
	ResumeStatus    ResumeStatus `json:"resumeStatus"`
	ResumeReason    *string      `json:"resumeReason,omitempty"`
	CreatedAt       time.Time    `json:"createdAt"`
	UpdatedAt       time.Time    `json:"updatedAt"`
}

// Job is a complete unit of agent work, from user input until completion,
// error, cancellation or a waiting interaction (spec section 3.5).
type Job struct {
	ID     JobID     `json:"id"`
	RunID  RunID     `json:"runId"`
	Status JobStatus `json:"status"`
	// IdempotencyKey carries the client request id used to make enqueueing
	// retry-safe (spec section 27).
	IdempotencyKey *string    `json:"-"`
	Error          *string    `json:"error,omitempty"`
	CreatedAt      time.Time  `json:"createdAt"`
	UpdatedAt      time.Time  `json:"updatedAt"`
	StartedAt      *time.Time `json:"startedAt,omitempty"`
	EndedAt        *time.Time `json:"endedAt,omitempty"`
}

// BackendInstance is an autonomous remote execution participant. It belongs to a
// User, not to a Project, and owns provider credentials, native sessions, the
// filesystem and local tools (spec sections 7 and 8).
type BackendInstance struct {
	ID BackendInstanceID `json:"id"`
	// OwnerID is nil while the instance is UNCLAIMED.
	OwnerID           *UserID                  `json:"ownerId,omitempty"`
	Name              string                   `json:"name"`
	OwnershipStatus   BackendOwnershipStatus   `json:"ownershipStatus"`
	OperationalStatus BackendOperationalStatus `json:"operationalStatus"`
	ProviderAuthState ProviderAuthState        `json:"providerAuthState"`
	Capabilities      []Capability             `json:"capabilities"`
	Capacity          Capacity                 `json:"capacity"`
	ProtocolVersion   int                      `json:"protocolVersion"`
	// ConnectionID is the lease of the currently active control stream
	// (spec section 9). It is nil when no connection is active.
	ConnectionID    *string    `json:"-"`
	LastHeartbeatAt *time.Time `json:"lastHeartbeatAt,omitempty"`
	CreatedAt       time.Time  `json:"createdAt"`
	UpdatedAt       time.Time  `json:"updatedAt"`
	RevokedAt       *time.Time `json:"revokedAt,omitempty"`
}

// HasCapability reports whether the instance advertises c.
func (b BackendInstance) HasCapability(c Capability) bool {
	for _, have := range b.Capabilities {
		if have == c {
			return true
		}
	}
	return false
}
