package postgres

import (
	"context"

	"github.com/rclsilver/threavia/internal/core/domain"
)

const knownDirectoryColumns = `d.id, d.project_id, d.name, d.description, d.git_remote,
	d.created_at, d.updated_at`

// CreateKnownDirectory inserts a project-level logical directory.
func (s *Store) CreateKnownDirectory(ctx context.Context, d *domain.KnownDirectory) error {
	err := s.q.QueryRow(ctx, `
		INSERT INTO known_directories (id, project_id, name, description, git_remote)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING created_at, updated_at`,
		d.ID, d.ProjectID, d.Name, d.Description, d.GitRemote,
	).Scan(&d.CreatedAt, &d.UpdatedAt)
	return classify(err, "create known directory")
}

// GetKnownDirectory returns a directory the user can access through its Project.
func (s *Store) GetKnownDirectory(ctx context.Context, ownerID domain.UserID, id domain.KnownDirectoryID) (domain.KnownDirectory, error) {
	return scanKnownDirectory(s.q.QueryRow(ctx, `
		SELECT `+knownDirectoryColumns+`
		FROM known_directories d JOIN projects p ON p.id = d.project_id
		WHERE d.id = $1 AND p.owner_id = $2`, id, ownerID))
}

// ListKnownDirectories returns the logical directories of a Project.
func (s *Store) ListKnownDirectories(ctx context.Context, ownerID domain.UserID, projectID domain.ProjectID) ([]domain.KnownDirectory, error) {
	rows, err := s.q.Query(ctx, `
		SELECT `+knownDirectoryColumns+`
		FROM known_directories d JOIN projects p ON p.id = d.project_id
		WHERE d.project_id = $1 AND p.owner_id = $2
		ORDER BY d.name`, projectID, ownerID)
	if err != nil {
		return nil, classify(err, "list known directories")
	}
	defer rows.Close()

	var out []domain.KnownDirectory
	for rows.Next() {
		dir, err := scanKnownDirectory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, dir)
	}
	return out, classify(rows.Err(), "list known directories")
}

// BindKnownDirectory records where a logical directory lives on one backend.
// Re-binding updates the path: the same directory legitimately moves.
func (s *Store) BindKnownDirectory(ctx context.Context, binding *domain.KnownDirectoryBinding) error {
	err := s.q.QueryRow(ctx, `
		INSERT INTO known_directory_bindings (known_directory_id, backend_instance_id, path)
		VALUES ($1, $2, $3)
		ON CONFLICT (known_directory_id, backend_instance_id)
		DO UPDATE SET path = EXCLUDED.path, updated_at = now()
		RETURNING created_at, updated_at`,
		binding.KnownDirectoryID, binding.BackendInstanceID, binding.Path,
	).Scan(&binding.CreatedAt, &binding.UpdatedAt)
	return classify(err, "bind known directory")
}

// ListBindings returns every backend binding of a logical directory.
func (s *Store) ListBindings(ctx context.Context, ownerID domain.UserID, id domain.KnownDirectoryID) ([]domain.KnownDirectoryBinding, error) {
	rows, err := s.q.Query(ctx, `
		SELECT b.known_directory_id, b.backend_instance_id, b.path, b.created_at, b.updated_at
		FROM known_directory_bindings b
		JOIN known_directories d ON d.id = b.known_directory_id
		JOIN projects p ON p.id = d.project_id
		WHERE b.known_directory_id = $1 AND p.owner_id = $2
		ORDER BY b.backend_instance_id`, id, ownerID)
	if err != nil {
		return nil, classify(err, "list bindings")
	}
	defer rows.Close()

	var out []domain.KnownDirectoryBinding
	for rows.Next() {
		var binding domain.KnownDirectoryBinding
		if err := rows.Scan(&binding.KnownDirectoryID, &binding.BackendInstanceID,
			&binding.Path, &binding.CreatedAt, &binding.UpdatedAt); err != nil {
			return nil, classify(err, "read binding")
		}
		out = append(out, binding)
	}
	return out, classify(rows.Err(), "list bindings")
}

// ResolveBinding returns the physical path of a logical directory on one
// backend. The same logical directory has different paths on different
// backends, which is the whole point of the indirection.
func (s *Store) ResolveBinding(ctx context.Context, id domain.KnownDirectoryID, instanceID domain.BackendInstanceID) (string, error) {
	var path string
	err := s.q.QueryRow(ctx, `
		SELECT path FROM known_directory_bindings
		WHERE known_directory_id = $1 AND backend_instance_id = $2`, id, instanceID).Scan(&path)
	return path, classify(err, "resolve binding")
}

func scanKnownDirectory(row scanner) (domain.KnownDirectory, error) {
	var dir domain.KnownDirectory
	err := row.Scan(&dir.ID, &dir.ProjectID, &dir.Name, &dir.Description, &dir.GitRemote,
		&dir.CreatedAt, &dir.UpdatedAt)
	return dir, classify(err, "read known directory")
}
