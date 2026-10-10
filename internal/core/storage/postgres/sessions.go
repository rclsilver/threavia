package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/rclsilver/threavia/internal/core/domain"
)

const sessionColumns = `s.id, s.project_id, s.title, s.status, s.working_directory_id,
	s.created_at, s.updated_at, s.archived_at, s.pinned_at, s.manager_session_id`

// CreateSession inserts a Session.
func (s *Store) CreateSession(ctx context.Context, session *domain.Session) error {
	err := s.q.QueryRow(ctx, `
		INSERT INTO sessions (id, project_id, title, status, working_directory_id, manager_session_id)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING created_at, updated_at`,
		session.ID, session.ProjectID, session.Title, session.Status, session.WorkingDirectoryID, session.ManagerSessionID,
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

// ListSessions returns the Sessions of a Project, most recently active first,
// each with the status of the Job holding it: the oldest one not yet finished,
// which is the one everything else in the Session is queued behind.
func (s *Store) ListSessions(ctx context.Context, ownerID domain.UserID, projectID domain.ProjectID, includeArchived bool) ([]domain.Session, error) {
	rows, err := s.q.Query(ctx, `
		SELECT `+sessionColumns+`, `+activeJobStatus+`
		FROM sessions s JOIN projects p ON p.id = s.project_id
		WHERE s.project_id = $1 AND p.owner_id = $2 AND ($3 OR s.status = 'ACTIVE')
		ORDER BY s.updated_at DESC`, projectID, ownerID, includeArchived)
	if err != nil {
		return nil, classify(err, "list sessions")
	}
	return readSessionList(rows)
}

// PinnedSessions returns the Sessions an owner pinned, in every Project, in
// the order they were pinned. An archived one is left out: it is out of the
// way on purpose, and it comes back pinned when it is restored.
func (s *Store) PinnedSessions(ctx context.Context, ownerID domain.UserID) ([]domain.Session, error) {
	rows, err := s.q.Query(ctx, `
		SELECT `+sessionColumns+`, `+activeJobStatus+`
		FROM sessions s JOIN projects p ON p.id = s.project_id
		WHERE p.owner_id = $1 AND s.pinned_at IS NOT NULL AND s.status = 'ACTIVE'
		ORDER BY s.pinned_at`, ownerID)
	if err != nil {
		return nil, classify(err, "list pinned sessions")
	}
	return readSessionList(rows)
}

// SetSessionPinned pins or unpins a Session. Pinning one already pinned keeps
// its place.
func (s *Store) SetSessionPinned(ctx context.Context, ownerID domain.UserID, id domain.SessionID, pinned bool) (domain.Session, error) {
	return scanSession(s.q.QueryRow(ctx, `
		UPDATE sessions s
		SET pinned_at = CASE WHEN $3 THEN coalesce(s.pinned_at, now()) END
		FROM projects p
		WHERE s.project_id = p.id AND s.id = $1 AND p.owner_id = $2
		RETURNING `+sessionColumns, id, ownerID, pinned))
}

// activeJobStatus is the status of the Job holding a Session: the oldest one
// not yet finished, which is the one everything else in it is queued behind.
const activeJobStatus = `(SELECT j.status FROM jobs j JOIN runs r ON r.id = j.run_id
		 WHERE r.session_id = s.id
		   AND j.status NOT IN ('COMPLETED', 'FAILED', 'CANCELLED')
		 ORDER BY j.created_at LIMIT 1)`

// readSessionList reads Sessions listed with the status of their active Job.
func readSessionList(rows pgx.Rows) ([]domain.Session, error) {
	defer rows.Close()

	var out []domain.Session
	for rows.Next() {
		var session domain.Session
		err := rows.Scan(&session.ID, &session.ProjectID, &session.Title, &session.Status,
			&session.WorkingDirectoryID, &session.CreatedAt, &session.UpdatedAt, &session.ArchivedAt, &session.PinnedAt,
			&session.ManagerSessionID,
			&session.ActiveJobStatus)
		if err != nil {
			return nil, classify(err, "read session")
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
		&session.WorkingDirectoryID, &session.CreatedAt, &session.UpdatedAt, &session.ArchivedAt, &session.PinnedAt, &session.ManagerSessionID)
	return session, classify(err, "read session")
}

// SessionPlace is where a Session's work happens: the backend of its current
// Run, and its working directory as bound on that backend.
type SessionPlace struct {
	BackendInstanceID domain.BackendInstanceID
	// KnownDirectoryName is set when the Session has a working directory, and
	// Path when that directory is located on the backend. A Session with
	// neither runs wherever the backend puts unscoped work.
	KnownDirectoryName *string
	Path               *string
}

// SessionPlace finds where a Session of this owner works now.
func (s *Store) SessionPlace(ctx context.Context, ownerID domain.UserID, id domain.SessionID) (SessionPlace, error) {
	var place SessionPlace
	err := s.q.QueryRow(ctx, `
		SELECT r.backend_instance_id, d.name, b.path
		FROM sessions s
		JOIN projects p ON p.id = s.project_id
		JOIN LATERAL (
			SELECT backend_instance_id FROM runs
			WHERE session_id = s.id ORDER BY created_at DESC LIMIT 1
		) r ON true
		LEFT JOIN known_directories d ON d.id = s.working_directory_id
		LEFT JOIN known_directory_bindings b
		       ON b.known_directory_id = s.working_directory_id
		      AND b.backend_instance_id = r.backend_instance_id
		WHERE s.id = $1 AND p.owner_id = $2`, id, ownerID).Scan(
		&place.BackendInstanceID, &place.KnownDirectoryName, &place.Path)
	if err != nil {
		return place, classify(err, "find where a session works")
	}
	return place, nil
}
