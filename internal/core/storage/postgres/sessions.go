package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/rclsilver/threavia/internal/core/domain"
)

const sessionColumns = `s.id, s.project_id, s.title, s.status, s.working_directory_id,
	s.created_at, s.updated_at, s.archived_at, s.pinned_at, s.manager_session_id, s.model, s.effort,
	s.read_sequence`

// CreateSession inserts a Session.
func (s *Store) CreateSession(ctx context.Context, session *domain.Session) error {
	err := s.q.QueryRow(ctx, `
		INSERT INTO sessions (id, project_id, title, status, working_directory_id, manager_session_id, model, effort)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING created_at, updated_at`,
		session.ID, session.ProjectID, session.Title, session.Status, session.WorkingDirectoryID, session.ManagerSessionID, session.Model, session.Effort,
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
		SELECT `+sessionColumns+`, `+activeJobStatus+`, `+sessionUnread+`
		FROM sessions s JOIN projects p ON p.id = s.project_id
		WHERE s.project_id = $1 AND p.owner_id = $2 AND ($3 OR s.status = 'ACTIVE')
		ORDER BY s.updated_at DESC`, projectID, ownerID, includeArchived)
	if err != nil {
		return nil, classify(err, "list sessions")
	}
	return readSessionList(rows)
}

// ToolSessions lists an owner's sessions across projects, with optional project
// and direct-manager filters. Creation order keeps pagination stable when an
// existing session receives messages. An empty filter means all owned projects.
func (s *Store) ToolSessions(ctx context.Context, ownerID domain.UserID, projectID domain.ProjectID, managerID domain.SessionID, includeArchived bool, limit, offset int) ([]domain.Session, error) {
	var project, manager any
	if projectID != "" {
		project = projectID
	}
	if managerID != "" {
		manager = managerID
	}
	rows, err := s.q.Query(ctx, `
		SELECT `+sessionColumns+`, `+activeJobStatus+`, `+sessionUnread+`
		FROM sessions s JOIN projects p ON p.id = s.project_id
		WHERE p.owner_id = $1
		  AND ($2::uuid IS NULL OR s.project_id = $2)
		  AND ($3::uuid IS NULL OR s.manager_session_id = $3)
		  AND ($4 OR (s.status = 'ACTIVE' AND p.status = 'ACTIVE'))
		ORDER BY s.created_at DESC, s.id DESC
		LIMIT $5 OFFSET $6`, ownerID, project, manager, includeArchived, limit, offset)
	if err != nil {
		return nil, classify(err, "list tool sessions")
	}
	return readSessionList(rows)
}

// ManagedSessions finds all direct delegates, including those in other projects,
// for the durable attention callback. It is not a paginated agent response.
func (s *Store) ManagedSessions(ctx context.Context, ownerID domain.UserID, managerID domain.SessionID) ([]domain.Session, error) {
	rows, err := s.q.Query(ctx, `
		SELECT `+sessionColumns+`, `+activeJobStatus+`, `+sessionUnread+`
		FROM sessions s JOIN projects p ON p.id = s.project_id
		WHERE p.owner_id = $1 AND s.manager_session_id = $2 AND s.status = 'ACTIVE'`, ownerID, managerID)
	if err != nil {
		return nil, classify(err, "list managed sessions")
	}
	return readSessionList(rows)
}

// PinnedSessions returns the Sessions an owner pinned, in every Project, in
// the order they were pinned. An archived one is left out: it is out of the
// way on purpose, and it comes back pinned when it is restored.
func (s *Store) PinnedSessions(ctx context.Context, ownerID domain.UserID) ([]domain.Session, error) {
	rows, err := s.q.Query(ctx, `
		SELECT `+sessionColumns+`, `+activeJobStatus+`, `+sessionUnread+`
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

// unreadTypes are what a person comes back to a Session for: something the
// agent said, or a Job that failed. Their own messages and the tools the agent
// ran on the way are not.
const unreadTypes = `('agent.message', 'job.failed')`

// sessionUnread says whether one of the unreadTypes came after the last event
// the person was shown.
const sessionUnread = `EXISTS (SELECT 1 FROM events e
		 WHERE e.session_id = s.id AND e.global_sequence > s.read_sequence
		   AND e.type IN ` + unreadTypes + `)`

// MarkSessionRead moves how far the person has read a Session up to sequence.
// It never moves back, and never past the last event of the Session, so a
// client cannot mark as read what has not happened yet. It returns the Session
// and whether the move cleared something unread, which is the only change the
// person's other clients have to hear about.
func (s *Store) MarkSessionRead(ctx context.Context, ownerID domain.UserID, id domain.SessionID, sequence domain.Sequence) (domain.Session, bool, error) {
	var cleared bool
	session, err := scanSession(s.q.QueryRow(ctx, `
		WITH target AS (
			SELECT s.id, s.read_sequence AS before, least($3, coalesce(
				(SELECT max(e.global_sequence) FROM events e WHERE e.session_id = s.id), 0)) AS sequence
			FROM sessions s JOIN projects p ON p.id = s.project_id
			WHERE s.id = $1 AND p.owner_id = $2
			FOR UPDATE OF s)
		UPDATE sessions s SET read_sequence = target.sequence
		FROM target
		WHERE s.id = target.id AND s.read_sequence < target.sequence
		RETURNING `+sessionColumns+`, EXISTS (SELECT 1 FROM events e
			WHERE e.session_id = s.id AND e.global_sequence > target.before
			  AND e.global_sequence <= target.sequence AND e.type IN `+unreadTypes+`)`,
		id, ownerID, sequence), &cleared)
	if !errors.Is(err, ErrNotFound) {
		return session, cleared, err
	}
	// Nothing moved: already read that far, or not this owner's Session.
	session, err = s.GetSession(ctx, ownerID, id)
	return session, false, err
}

// readSessionList reads Sessions listed with the status of their active Job
// and whether they hold something unread.
func readSessionList(rows pgx.Rows) ([]domain.Session, error) {
	defer rows.Close()

	var out []domain.Session
	for rows.Next() {
		var session domain.Session
		err := rows.Scan(&session.ID, &session.ProjectID, &session.Title, &session.Status,
			&session.WorkingDirectoryID, &session.CreatedAt, &session.UpdatedAt, &session.ArchivedAt, &session.PinnedAt,
			&session.ManagerSessionID, &session.Model, &session.Effort, &session.ReadSequence,
			&session.ActiveJobStatus, &session.Unread)
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

// scanSession reads the sessionColumns, then any extra column the query added.
func scanSession(row scanner, extra ...any) (domain.Session, error) {
	var session domain.Session
	err := row.Scan(append([]any{&session.ID, &session.ProjectID, &session.Title, &session.Status,
		&session.WorkingDirectoryID, &session.CreatedAt, &session.UpdatedAt, &session.ArchivedAt, &session.PinnedAt, &session.ManagerSessionID, &session.Model, &session.Effort,
		&session.ReadSequence}, extra...)...)
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
