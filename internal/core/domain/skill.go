package domain

import "time"

// SkillID identifies a Core-managed Project Skill.
type SkillID string

// NewSkillID returns a fresh SkillID.
func NewSkillID() SkillID { return SkillID(NewUUID()) }

// SkillSourceType is where a Skill was acquired from (spec section 18).
type SkillSourceType string

const (
	// SkillSourceGit clones a repository at an optional ref and path.
	SkillSourceGit SkillSourceType = "GIT"
	// SkillSourceArchive downloads a .zip or .tar.gz.
	SkillSourceArchive SkillSourceType = "ARCHIVE"
	// SkillSourceUpload is an archive the user sent directly.
	SkillSourceUpload SkillSourceType = "UPLOAD"
)

// Valid reports whether the source type is one Core knows how to acquire.
func (t SkillSourceType) Valid() bool {
	switch t {
	case SkillSourceGit, SkillSourceArchive, SkillSourceUpload:
		return true
	default:
		return false
	}
}

// SkillSource records what was asked for. It is provenance, kept beside what was
// actually installed, so a Skill can be re-acquired and compared later.
type SkillSource struct {
	Type SkillSourceType `json:"type"`
	URL  string          `json:"url,omitempty"`
	// Path selects a subdirectory of the repository or archive.
	Path string `json:"path,omitempty"`
	// Revision is the requested branch, tag or commit. It may be a moving
	// reference; InstalledRevision never is.
	Revision string `json:"revision,omitempty"`
}

// Skill is a Core-managed Project Skill: an immutable, versioned artefact with
// provenance (spec section 18).
//
// Core acquires and stores it; it never runs anything the bundle contains.
type Skill struct {
	ID          SkillID   `json:"id"`
	OwnerID     UserID    `json:"ownerId"`
	ProjectID   ProjectID `json:"projectId"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`

	Source SkillSource `json:"source"`
	// InstalledRevision is the immutable identity of what was installed: a
	// commit for a repository, a content hash otherwise.
	InstalledRevision string    `json:"installedRevision"`
	InstalledAt       time.Time `json:"installedAt"`

	// ArtifactID points at the packed bundle; BundleSHA256 lets a backend tell a
	// cached copy from a stale one without asking for the bytes.
	ArtifactID   ArtifactID `json:"artifactId"`
	BundleSHA256 string     `json:"bundleSha256"`

	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// BackendSkill is a Skill that exists only on a BackendInstance. Core knows its
// metadata and never its content, which is what lets a backend expose a Skill
// that only exists behind a corporate network (spec section 18).
type BackendSkill struct {
	BackendInstanceID BackendInstanceID `json:"backendInstanceId"`
	Name              string            `json:"name"`
	Description       string            `json:"description,omitempty"`
	Available         bool              `json:"available"`
	ReportedAt        time.Time         `json:"reportedAt"`
}
