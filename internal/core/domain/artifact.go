package domain

import "time"

// ArtifactID identifies an Artifact.
type ArtifactID string

// NewArtifactID returns a fresh ArtifactID.
func NewArtifactID() ArtifactID { return ArtifactID(NewUUID()) }

// Artifact is a stored blob: an upload, a screenshot, a large log, a generated
// report (spec section 19).
//
// Its metadata lives in PostgreSQL and its bytes in object storage. Events
// reference it by identifier rather than embedding it, which is what keeps a
// timeline pageable.
type Artifact struct {
	ID        ArtifactID `json:"id"`
	OwnerID   UserID     `json:"ownerId"`
	ProjectID ProjectID  `json:"projectId"`
	// Filename is metadata only. The object key is derived from identifiers, so
	// nothing a caller names can shape where the bytes land.
	Filename  string     `json:"filename"`
	MimeType  string     `json:"mimeType"`
	Size      int64      `json:"size"`
	SHA256    string     `json:"sha256"`
	ObjectKey string     `json:"-"`
	SessionID *SessionID `json:"sessionId,omitempty"`
	JobID     *JobID     `json:"jobId,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
}
