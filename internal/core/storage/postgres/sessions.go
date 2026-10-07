package postgres

import (
	"context"
	"time"

	"github.com/rclsilver/threavia/internal/core/domain"
)

const sessionColumns = `s.id, s.project_id, s.title, s.status, s.working_directory_id,
	s.created_at, s.updated_at, s.archived_at`

// CreateSession inserts a Session.
func (s *Store) CreateSession(ctx context.Context, session *domain.Session) error {
	err := s.q.QueryRow(ctx, `
		INSERT INTO sessions (id, project_id, title, status, working_directory_id)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING created_at, updated_at`,
		session.ID, session.ProjectID, session.Title, session.Status, session.WorkingDirectoryID,
	).Scan(&session.CreatedAt, &session.UpdatedAt)
	return classify(err, "create session")
}

// GetSession returns a Session the user can access through its Project.
func (s *Store) GetSession(ctx context.Context, ownerID domain.UserID, id domain.SessionID) (domain.Session, error) {
	return scanSession(s.q.QueryRow(ctx, `
		SELECT `+sessionColumns+`
		FROM sessions s JOIN projects p ON p.id = s.project_id
		WHERE s.id = $1 AND p.owner_id = $2`, id, ownerID))
}

// DeleteSession removes a Session for good.
//
// Its Runs, Jobs, events and pending requests go with it, by the cascade the
// schema declares. Artifacts do not: a file the work produced belongs to the
// Project and outlives the conversation that made it.
func (s *Store) DeleteSession(ctx context.Context, ownerID domain.UserID, id domain.SessionID) error {
	tag, err := s.q.Exec(ctx, `
		DELETE FROM sessions s
		USING projects p
		WHERE s.id = $1 AND s.project_id = p.id AND p.owner_id = $2`, id, ownerID)
	if err != nil {
		return classify(err, "delete a session")
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListSessions returns the Sessions of a Project, most recently active first.
func (s *Store) ListSessions(ctx context.Context, ownerID domain.UserID, projectID domain.ProjectID, includeArchived bool) ([]domain.Session, error) {
	rows, err := s.q.Query(ctx, `
		SELECT `+sessionColumns+`
		FROM sessions s JOIN projects p ON p.id = s.project_id
		WHERE s.project_id = $1 AND p.owner_id = $2 AND ($3 OR s.status = 'ACTIVE')
		ORDER BY s.updated_at DESC`, projectID, ownerID, includeArchived)
	if err != nil {
		return nil, classify(err, "list sessions")
	}
	defer rows.Close()

	var out []domain.Session
	for rows.Next() {
		session, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, session)
	}
	return out, classify(rows.Err(), "list sessions")
}

// RenameSession sets a user-chosen title. The title has no technical meaning.
func (s *Store) RenameSession(ctx context.Context, ownerID domain.UserID, id domain.SessionID, title string) (domain.Session, error) {
	return scanSession(s.q.QueryRow(ctx, `
		UPDATE sessions s SET title = $3, updated_at = now()
		FROM projects p
		WHERE s.project_id = p.id AND s.id = $1 AND p.owner_id = $2
		RETURNING `+sessionColumns, id, ownerID, title))
}

// SetSessionStatus archives or restores a Session.
func (s *Store) SetSessionStatus(ctx context.Context, ownerID domain.UserID, id domain.SessionID, status domain.SessionStatus) (domain.Session, error) {
	var archivedAt *time.Time
	if status == domain.SessionArchived {
		now := time.Now().UTC()
		archivedAt = &now
	}
	return scanSession(s.q.QueryRow(ctx, `
		UPDATE sessions s SET status = $3, archived_at = $4, updated_at = now()
		FROM projects p
		WHERE s.project_id = p.id AND s.id = $1 AND p.owner_id = $2
		RETURNING `+sessionColumns, id, ownerID, status, archivedAt))
}

// SetSessionWorkingDirectory changes the initial cwd of a Session. The change is
// always explicit: a temporary cd by the agent never mutates it.
func (s *Store) SetSessionWorkingDirectory(ctx context.Context, ownerID domain.UserID, id domain.SessionID, dir *domain.KnownDirectoryID) (domain.Session, error) {
	return scanSession(s.q.QueryRow(ctx, `
		UPDATE sessions s SET working_directory_id = $3, updated_at = now()
		FROM projects p
		WHERE s.project_id = p.id AND s.id = $1 AND p.owner_id = $2
		RETURNING `+sessionColumns, id, ownerID, dir))
}

// TouchSession bumps the Session update time.
func (s *Store) TouchSession(ctx context.Context, id domain.SessionID) error {
	_, err := s.q.Exec(ctx, `UPDATE sessions SET updated_at = now() WHERE id = $1`, id)
	return classify(err, "touch session")
}

func scanSession(row scanner) (domain.Session, error) {
	var session domain.Session
	err := row.Scan(&session.ID, &session.ProjectID, &session.Title, &session.Status,
		&session.WorkingDirectoryID, &session.CreatedAt, &session.UpdatedAt, &session.ArchivedAt)
	return session, classify(err, "read session")
}
