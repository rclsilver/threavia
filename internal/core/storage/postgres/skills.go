package postgres

import (
	"context"

	"github.com/rclsilver/threavia/internal/core/domain"
)

const skillColumns = `s.id, s.owner_id, COALESCE(s.project_id::text, ''), s.name, s.description,
	s.source_type, s.source_url, s.source_path, s.source_revision,
	s.installed_revision, s.installed_at, s.artifact_id, s.bundle_sha256,
	s.created_at, s.updated_at`

// LockProjectSkills serializes installs and removals, including an absent
// skill's first install, so replacement metadata describes the committed row.
func (s *Store) LockProjectSkills(ctx context.Context, ownerID domain.UserID, projectID domain.ProjectID) error {
	var id domain.ProjectID
	err := s.q.QueryRow(ctx, `SELECT id FROM projects WHERE id = $1 AND owner_id = $2 FOR UPDATE`, projectID, ownerID).Scan(&id)
	return classify(err, "lock project skills")
}

// LockGlobalSkills does for the global Skills of an owner what
// LockProjectSkills does for a Project's.
func (s *Store) LockGlobalSkills(ctx context.Context, ownerID domain.UserID) error {
	_, err := s.q.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('global-skills:' || $1::text, 0))`, ownerID)
	return classify(err, "lock global skills")
}

// UpsertSkill installs a Skill, replacing an earlier version of the same name in
// the same Project, or among the owner's global Skills when it has no Project.
//
// It returns the Artifact the Skill referenced before, when it replaced one, so
// the caller can remove a bundle nothing points at any more.
//
// Installing a new version is an update, not a second row: a Project has one
// Skill under a given name, and its history is the chain of immutable bundles in
// object storage.
func (s *Store) UpsertSkill(ctx context.Context, skill *domain.Skill) (*domain.ArtifactID, error) {
	// A name is unique within a Project, and among the global Skills of an owner.
	target := `(project_id, name)`
	if skill.ProjectID == "" {
		target = `(owner_id, name) WHERE project_id IS NULL`
	}
	// The CTE reads the previous row against the statement snapshot, so it names
	// the bundle that is being replaced rather than the one being written.
	var replaced *domain.ArtifactID
	err := s.q.QueryRow(ctx, `
		WITH previous AS (
			SELECT artifact_id FROM skills
			WHERE owner_id = $2 AND project_id IS NOT DISTINCT FROM NULLIF($3::text, '')::uuid AND name = $4
		), upserted AS (
			INSERT INTO skills (id, owner_id, project_id, name, description,
			                    source_type, source_url, source_path, source_revision,
			                    installed_revision, artifact_id, bundle_sha256)
			VALUES ($1, $2, NULLIF($3::text, '')::uuid, $4, $5, $6, $7, $8, $9, $10, $11, $12)
			ON CONFLICT `+target+` DO UPDATE SET
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

// ListSkills returns the Skills of a Project, by name, without the global ones.
func (s *Store) ListSkills(ctx context.Context, projectID domain.ProjectID) ([]domain.Skill, error) {
	rows, err := s.q.Query(ctx,
		`SELECT `+skillColumns+` FROM skills s WHERE s.project_id = $1 ORDER BY s.name`, projectID)
	if err != nil {
		return nil, classify(err, "list skills")
	}
	defer rows.Close()

	return collect(rows, scanSkill, "list skills")
}

// ListGlobalSkills returns the global Skills of an owner, by name, each with the
// Projects that turned it off.
func (s *Store) ListGlobalSkills(ctx context.Context, ownerID domain.UserID) ([]domain.Skill, error) {
	rows, err := s.q.Query(ctx, `
		SELECT `+skillColumns+`,
		       COALESCE((SELECT array_agg(x.project_id::text ORDER BY x.project_id)
		                 FROM skill_project_exclusions x WHERE x.skill_id = s.id), '{}')
		FROM skills s WHERE s.project_id IS NULL AND s.owner_id = $1 ORDER BY s.name`, ownerID)
	if err != nil {
		return nil, classify(err, "list global skills")
	}
	defer rows.Close()

	return collect(rows, scanGlobalSkill, "list global skills")
}

// ActiveGlobalSkills returns the global Skills of the Project's owner that the
// Project did not turn off, by name.
func (s *Store) ActiveGlobalSkills(ctx context.Context, projectID domain.ProjectID) ([]domain.Skill, error) {
	rows, err := s.q.Query(ctx, `
		SELECT `+skillColumns+`
		FROM skills s JOIN projects p ON p.owner_id = s.owner_id
		WHERE p.id = $1 AND s.project_id IS NULL
		  AND NOT EXISTS (SELECT 1 FROM skill_project_exclusions x
		                  WHERE x.skill_id = s.id AND x.project_id = p.id)
		ORDER BY s.name`, projectID)
	if err != nil {
		return nil, classify(err, "list active global skills")
	}
	defer rows.Close()

	return collect(rows, scanSkill, "list active global skills")
}

// MakeSkillGlobal moves a Project Skill to its owner's global Skills, with its
// bundle, so deleting the Project no longer removes what other Projects use.
// A global Skill of the same name is a conflict.
func (s *Store) MakeSkillGlobal(ctx context.Context, ownerID domain.UserID, id domain.SkillID) (domain.Skill, error) {
	skill, err := scanSkill(s.q.QueryRow(ctx, `
		UPDATE skills s SET project_id = NULL, updated_at = now()
		WHERE s.id = $1 AND s.owner_id = $2
		RETURNING `+skillColumns, id, ownerID))
	if err != nil {
		return skill, err
	}
	_, err = s.q.Exec(ctx, `UPDATE artifacts SET project_id = NULL WHERE id = $1`, skill.ArtifactID)
	return skill, classify(err, "make skill bundle global")
}

// SetSkillProjectEnabled turns a global Skill on or off in one Project.
func (s *Store) SetSkillProjectEnabled(ctx context.Context, id domain.SkillID, projectID domain.ProjectID, enabled bool) error {
	query := `INSERT INTO skill_project_exclusions (skill_id, project_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`
	if enabled {
		query = `DELETE FROM skill_project_exclusions WHERE skill_id = $1 AND project_id = $2`
	}
	_, err := s.q.Exec(ctx, query, id, projectID)
	return classify(err, "set skill project activation")
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
	if s.pool == nil {
		return s.replaceBackendSkills(ctx, backendID, skills)
	}
	return s.WithTx(ctx, func(tx *Store) error {
		return tx.replaceBackendSkills(ctx, backendID, skills)
	})
}

func (s *Store) replaceBackendSkills(ctx context.Context, backendID domain.BackendInstanceID, skills []domain.BackendSkill) error {
	if _, err := s.q.Exec(ctx,
		`DELETE FROM backend_skills WHERE backend_instance_id = $1`, backendID); err != nil {
		return classify(err, "replace backend skills")
	}
	for _, skill := range skills {
		if _, err := s.q.Exec(ctx, `
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

func scanGlobalSkill(row scanner) (domain.Skill, error) {
	var (
		s        domain.Skill
		disabled []string
	)
	err := row.Scan(&s.ID, &s.OwnerID, &s.ProjectID, &s.Name, &s.Description,
		&s.Source.Type, &s.Source.URL, &s.Source.Path, &s.Source.Revision,
		&s.InstalledRevision, &s.InstalledAt, &s.ArtifactID, &s.BundleSHA256,
		&s.CreatedAt, &s.UpdatedAt, &disabled)
	for _, id := range disabled {
		s.DisabledProjectIDs = append(s.DisabledProjectIDs, domain.ProjectID(id))
	}
	return s, classify(err, "read global skill")
}

func scanBackendSkill(row scanner) (domain.BackendSkill, error) {
	var s domain.BackendSkill
	err := row.Scan(&s.BackendInstanceID, &s.Name, &s.Description, &s.Available, &s.ReportedAt)
	return s, classify(err, "read backend skill")
}
