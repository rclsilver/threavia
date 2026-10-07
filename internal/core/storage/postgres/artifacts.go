package postgres

import (
	"context"

	"github.com/rclsilver/threavia/internal/core/domain"
)

const artifactColumns = `a.id, a.owner_id, a.project_id, a.filename, a.mime_type,
	a.size, a.sha256, a.object_key, a.session_id, a.job_id, a.created_at`

// CreateArtifact records the metadata of a stored blob.
func (s *Store) CreateArtifact(ctx context.Context, artifact *domain.Artifact) error {
	err := s.q.QueryRow(ctx, `
		INSERT INTO artifacts (id, owner_id, project_id, filename, mime_type,
		                       size, sha256, object_key, session_id, job_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING created_at`,
		artifact.ID, artifact.OwnerID, artifact.ProjectID, artifact.Filename,
		artifact.MimeType, artifact.Size, artifact.SHA256, artifact.ObjectKey,
		artifact.SessionID, artifact.JobID,
	).Scan(&artifact.CreatedAt)
	return classify(err, "create artifact")
}

// GetArtifact returns an Artifact the user owns.
func (s *Store) GetArtifact(ctx context.Context, ownerID domain.UserID, id domain.ArtifactID) (domain.Artifact, error) {
	return scanArtifact(s.q.QueryRow(ctx, `
		SELECT `+artifactColumns+`
		FROM artifacts a WHERE a.id = $1 AND a.owner_id = $2`, id, ownerID))
}

// ListArtifacts returns the Artifacts of a Project, most recent first.
//
// Skill bundles are left out: they are the content of an installed Skill, not
// something a user uploaded, and offering to delete one would only produce a
// conflict with the Skill that points at it.
func (s *Store) ListArtifacts(ctx context.Context, ownerID domain.UserID, projectID domain.ProjectID, limit int) ([]domain.Artifact, error) {
	rows, err := s.q.Query(ctx, `
		SELECT `+artifactColumns+`
		FROM artifacts a JOIN projects p ON p.id = a.project_id
		WHERE a.project_id = $1 AND p.owner_id = $2
		  AND NOT EXISTS (SELECT 1 FROM skills s WHERE s.artifact_id = a.id)
		ORDER BY a.created_at DESC
		LIMIT $3`, projectID, ownerID, limit)
	if err != nil {
		return nil, classify(err, "list artifacts")
	}
	defer rows.Close()

	return collect(rows, scanArtifact, "list artifacts")
}

// DeleteArtifact removes the metadata and returns the object key, so the caller
// can remove the bytes it describes.
func (s *Store) DeleteArtifact(ctx context.Context, ownerID domain.UserID, id domain.ArtifactID) (string, error) {
	var key string
	err := s.q.QueryRow(ctx,
		`DELETE FROM artifacts WHERE id = $1 AND owner_id = $2 RETURNING object_key`, id, ownerID).Scan(&key)
	return key, classify(err, "delete artifact")
}

func scanArtifact(row scanner) (domain.Artifact, error) {
	var a domain.Artifact
	err := row.Scan(&a.ID, &a.OwnerID, &a.ProjectID, &a.Filename, &a.MimeType,
		&a.Size, &a.SHA256, &a.ObjectKey, &a.SessionID, &a.JobID, &a.CreatedAt)
	return a, classify(err, "read artifact")
}
