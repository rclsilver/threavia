package domain

import "time"

// KnownDirectoryID identifies a KnownDirectory.
type KnownDirectoryID string

// NewKnownDirectoryID returns a fresh KnownDirectoryID.
func NewKnownDirectoryID() KnownDirectoryID { return KnownDirectoryID(NewUUID()) }

// KnownDirectory is a project-level logical directory (spec section 11).
//
// Git repositories and worktrees are deliberately not Core objects. A
// KnownDirectory is portability and discovery metadata, never a sandbox: the
// agent may work in any directory the backend filesystem allows.
type KnownDirectory struct {
	ID          KnownDirectoryID `json:"id"`
	ProjectID   ProjectID        `json:"projectId"`
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	GitRemote   *string          `json:"gitRemote,omitempty"`
	CreatedAt   time.Time        `json:"createdAt"`
	UpdatedAt   time.Time        `json:"updatedAt"`
}

// KnownDirectoryBinding is where a logical directory actually lives on one
// BackendInstance. The same directory has different paths on different
// backends.
type KnownDirectoryBinding struct {
	KnownDirectoryID  KnownDirectoryID  `json:"knownDirectoryId"`
	BackendInstanceID BackendInstanceID `json:"backendInstanceId"`
	Path              string            `json:"path"`
	CreatedAt         time.Time         `json:"createdAt"`
	UpdatedAt         time.Time         `json:"updatedAt"`
}
