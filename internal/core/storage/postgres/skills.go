package postgres

import (
	"context"

	"github.com/rclsilver/threavia/internal/core/domain"
)

const skillColumns = `s.id, s.owner_id, s.project_id, s.name, s.description,
	s.source_type, s.source_url, s.source_path, s.source_revision,
	s.installed_revision, s.installed_at, s.artifact_id, s.bundle_sha256,
	s.created_at, s.updated_at`

// UpsertSkill installs a Skill, replacing an earlier version of the same name in
// the same Project.
//
// It returns the Artifact the Skill referenced before, when it replaced one, so
// the caller can remove a bundle nothing points at any more.
//
// Installing a new version is an update, not a second row: a Project has one
// Skill under a given name, and its history is the chain of immutable bundles in
// object storage.
func (s *Store) UpsertSkill(ctx context.Context, skill *domain.Skill) (*domain.ArtifactID, error) {
	// The CTE reads the previous row against the statement snapshot, so it names
	// the bundle that is being replaced rather than the one being written.
	var replaced *domain.ArtifactID
	err := s.q.QueryRow(ctx, `
		WITH previous AS (
			SELECT artifact_id FROM skills WHERE project_id = $3 AND name = $4
		), upserted AS (
			INSERT INTO skills (id, owner_id, project_id, name, description,
			                    source_type, source_url, source_path, source_revision,
			                    installed_revision, artifact_id, bundle_sha256)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
			ON CONFLICT (project_id, name) DO UPDATE SET
				description        = EXCLUDED.description,
				source_type        = EXCLUDED.source_type,
				source_url         = EXCLUDED.source_url,
				source_path        = EXCLUDED.source_path,
				source_revision    = EXCLUDED.source_revision,
				installed_revision = EXCLUDED.installed_revision,
				installed_at       = now(),
				artifact_id        = EXCLUDED.artifact_id,
				bundle_sha256      = EXCLUDED.bundle_sha256,
				updated_at         = now()
			RETURNING id, installed_at, created_at, updated_at
		)
		SELECT u.id, u.installed_at, u.created_at, u.updated_at, p.artifact_id
		FROM upserted u LEFT JOIN previous p ON true`,
		skill.ID, skill.OwnerID, skill.ProjectID, skill.Name, skill.Description,
		skill.Source.Type, skill.Source.URL, skill.Source.Path, skill.Source.Revision,
		skill.InstalledRevision, skill.ArtifactID, skill.BundleSHA256,
	).Scan(&skill.ID, &skill.InstalledAt, &skill.CreatedAt, &skill.UpdatedAt, &replaced)
	if err != nil {
		return nil, classify(err, "install skill")
	}
	if replaced != nil && *replaced == skill.ArtifactID {
		return nil, nil
	}
	return replaced, nil
}

// GetSkill returns a Skill the user owns.
func (s *Store) GetSkill(ctx context.Context, ownerID domain.UserID, id domain.SkillID) (domain.Skill, error) {
	return scanSkill(s.q.QueryRow(ctx,
		`SELECT `+skillColumns+` FROM skills s WHERE s.id = $1 AND s.owner_id = $2`, id, ownerID))
}

// SkillByID returns a Skill without an ownership check. It serves the backend
// distribution path, where the caller is a BackendInstance rather than a user
// and the Job already established which Project is in play.
func (s *Store) SkillByID(ctx context.Context, id domain.SkillID) (domain.Skill, error) {
	return scanSkill(s.q.QueryRow(ctx, `SELECT `+skillColumns+` FROM skills s WHERE s.id = $1`, id))
}

// ListSkills returns the Skills of a Project, by name.
func (s *Store) ListSkills(ctx context.Context, projectID domain.ProjectID) ([]domain.Skill, error) {
	rows, err := s.q.Query(ctx,
		`SELECT `+skillColumns+` FROM skills s WHERE s.project_id = $1 ORDER BY s.name`, projectID)
	if err != nil {
		return nil, classify(err, "list skills")
	}
	defer rows.Close()

	return collect(rows, scanSkill, "list skills")
}

// DeleteSkill uninstalls a Skill and returns the bundle it referenced, so the
// caller can decide what to do with the bytes.
func (s *Store) DeleteSkill(ctx context.Context, ownerID domain.UserID, id domain.SkillID) (domain.ArtifactID, error) {
	var artifactID domain.ArtifactID
	err := s.q.QueryRow(ctx,
		`DELETE FROM skills WHERE id = $1 AND owner_id = $2 RETURNING artifact_id`, id, ownerID).Scan(&artifactID)
	return artifactID, classify(err, "delete skill")
}

// ReplaceBackendSkills records what a BackendInstance reports it has locally.
//
// The report is authoritative and complete: a Skill the backend no longer lists
// is gone, which is how Core stops offering work that depends on it.
func (s *Store) ReplaceBackendSkills(ctx context.Context, backendID domain.BackendInstanceID, skills []domain.BackendSkill) error {
	return s.WithTx(ctx, func(tx *Store) error {
		if _, err := tx.q.Exec(ctx,
			`DELETE FROM backend_skills WHERE backend_instance_id = $1`, backendID); err != nil {
			return classify(err, "replace backend skills")
		}
		for _, skill := range skills {
			if _, err := tx.q.Exec(ctx, `
				INSERT INTO backend_skills (backend_instance_id, name, description, available)
				VALUES ($1, $2, $3, $4)
				ON CONFLICT (backend_instance_id, name) DO UPDATE SET
					description = EXCLUDED.description,
					available   = EXCLUDED.available,
					reported_at = now()`,
				backendID, skill.Name, skill.Description, skill.Available); err != nil {
				return classify(err, "replace backend skills")
			}
		}
		return nil
	})
}

// ListBackendSkills returns the local Skills a BackendInstance reported.
func (s *Store) ListBackendSkills(ctx context.Context, backendID domain.BackendInstanceID) ([]domain.BackendSkill, error) {
	rows, err := s.q.Query(ctx, `
		SELECT backend_instance_id, name, description, available, reported_at
		FROM backend_skills WHERE backend_instance_id = $1 ORDER BY name`, backendID)
	if err != nil {
		return nil, classify(err, "list backend skills")
	}
	defer rows.Close()

	return collect(rows, scanBackendSkill, "list backend skills")
}

func scanSkill(row scanner) (domain.Skill, error) {
	var s domain.Skill
	err := row.Scan(&s.ID, &s.OwnerID, &s.ProjectID, &s.Name, &s.Description,
		&s.Source.Type, &s.Source.URL, &s.Source.Path, &s.Source.Revision,
		&s.InstalledRevision, &s.InstalledAt, &s.ArtifactID, &s.BundleSHA256,
		&s.CreatedAt, &s.UpdatedAt)
	return s, classify(err, "read skill")
}

func scanBackendSkill(row scanner) (domain.BackendSkill, error) {
	var s domain.BackendSkill
	err := row.Scan(&s.BackendInstanceID, &s.Name, &s.Description, &s.Available, &s.ReportedAt)
	return s, classify(err, "read backend skill")
}
