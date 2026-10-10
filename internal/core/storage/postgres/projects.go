package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/rclsilver/threavia/internal/core/domain"
)

const projectColumns = `id, owner_id, name, description, instructions, status,
	created_at, updated_at, archived_at`

// CreateProject inserts a Project.
func (s *Store) CreateProject(ctx context.Context, p *domain.Project) error {
	err := s.q.QueryRow(ctx, `
		INSERT INTO projects (id, owner_id, name, description, status)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING created_at, updated_at`,
		p.ID, p.OwnerID, p.Name, p.Description, p.Status,
	).Scan(&p.CreatedAt, &p.UpdatedAt)
	return classify(err, "create project")
}

// GetProject returns a Project owned by ownerID.
func (s *Store) GetProject(ctx context.Context, ownerID domain.UserID, id domain.ProjectID) (domain.Project, error) {
	return scanProject(s.q.QueryRow(ctx,
		`SELECT `+projectColumns+` FROM projects WHERE id = $1 AND owner_id = $2`, id, ownerID))
}

// ListProjects returns the Projects of a user, most recently updated first.
func (s *Store) ListProjects(ctx context.Context, ownerID domain.UserID, includeArchived bool) ([]domain.Project, error) {
	rows, err := s.q.Query(ctx, `
		SELECT `+projectColumns+`
		FROM projects
		WHERE owner_id = $1 AND ($2 OR status = 'ACTIVE')
		ORDER BY updated_at DESC`, ownerID, includeArchived)
	if err != nil {
		return nil, classify(err, "list projects")
	}
	defer rows.Close()

	var out []domain.Project
	for rows.Next() {
		project, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, project)
	}
	return out, classify(rows.Err(), "list projects")
}

// SetProjectStatus archives or restores a Project.
func (s *Store) SetProjectStatus(ctx context.Context, ownerID domain.UserID, id domain.ProjectID, status domain.ProjectStatus) (domain.Project, error) {
	var archivedAt *time.Time
	if status == domain.ProjectArchived {
		now := time.Now().UTC()
		archivedAt = &now
	}
	return scanProject(s.q.QueryRow(ctx, `
		UPDATE projects
		SET status = $3, archived_at = $4, updated_at = now()
		WHERE id = $1 AND owner_id = $2
		RETURNING `+projectColumns, id, ownerID, status, archivedAt))
}

// TouchProject bumps the Project update time, so listings order by real
// activity rather than by creation.
func (s *Store) TouchProject(ctx context.Context, id domain.ProjectID) error {
	_, err := s.q.Exec(ctx, `UPDATE projects SET updated_at = now() WHERE id = $1`, id)
	return classify(err, "touch project")
}

// DeleteProject permanently removes a Project and everything it owns. Archiving
// is the normal lifecycle; this is the explicit, separate operation of
// specification section 21.
func (s *Store) DeleteProject(ctx context.Context, ownerID domain.UserID, id domain.ProjectID) error {
	_, err := s.DeleteProjectData(ctx, ownerID, id)
	return err
}

// DeleteProjectData serializes deletion with new Jobs, and returns object keys
// for cleanup after commit. Backend filesystem contents are never touched.
func (s *Store) DeleteProjectData(ctx context.Context, ownerID domain.UserID, id domain.ProjectID) ([]string, error) {
	var keys []string
	err := s.WithTx(ctx, func(tx *Store) error {
		var locked domain.ProjectID
		if err := tx.q.QueryRow(ctx, `SELECT id FROM projects WHERE id = $1 AND owner_id = $2 FOR UPDATE`, id, ownerID).Scan(&locked); err != nil {
			return classify(err, "lock project")
		}
		var unfinished bool
		if err := tx.q.QueryRow(ctx, `SELECT EXISTS (
			SELECT 1 FROM jobs j JOIN runs r ON r.id = j.run_id JOIN sessions s ON s.id = r.session_id
			WHERE s.project_id = $1 AND j.status NOT IN ('COMPLETED','FAILED','CANCELLED'))`, id).Scan(&unfinished); err != nil {
			return classify(err, "read project work")
		}
		if unfinished {
			return fmt.Errorf("%w: this project has unfinished jobs; stop them before deleting it", ErrConflict)
		}
		rows, err := tx.q.Query(ctx, `SELECT object_key FROM artifacts WHERE project_id = $1`, id)
		if err != nil {
			return classify(err, "read project artifact keys")
		}
		for rows.Next() {
			var key string
			if err := rows.Scan(&key); err != nil {
				rows.Close()
				return err
			}
			keys = append(keys, key)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		_, err = tx.q.Exec(ctx, `DELETE FROM projects WHERE id = $1 AND owner_id = $2`, id, ownerID)
		return classify(err, "delete project")
	})
	return keys, err
}

type scanner interface{ Scan(dest ...any) error }

func scanProject(row scanner) (domain.Project, error) {
	var p domain.Project
	err := row.Scan(&p.ID, &p.OwnerID, &p.Name, &p.Description, &p.Instructions, &p.Status,
		&p.CreatedAt, &p.UpdatedAt, &p.ArchivedAt)
	return p, classify(err, "read project")
}

// UpdateProject changes the editable fields of a Project.
func (s *Store) UpdateProject(ctx context.Context, ownerID domain.UserID, id domain.ProjectID, name, description, instructions string) (domain.Project, error) {
	return scanProject(s.q.QueryRow(ctx, `
		UPDATE projects
		SET name = $3, description = $4, instructions = $5, updated_at = now()
		WHERE id = $1 AND owner_id = $2
		RETURNING `+projectColumns, id, ownerID, name, description, instructions))
}
