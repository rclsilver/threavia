package postgres

import (
	"context"

	"github.com/rclsilver/threavia/internal/core/domain"
)

const runColumns = `r.id, r.session_id, r.backend_instance_id, r.native_session_id,
	r.resume_status, r.resume_reason, r.created_at, r.updated_at`

// CreateRun inserts a Run, the binding between a Session and one
// BackendInstance.
func (s *Store) CreateRun(ctx context.Context, run *domain.Run) error {
	err := s.q.QueryRow(ctx, `
		INSERT INTO runs (id, session_id, backend_instance_id, native_session_id, resume_status)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING created_at, updated_at`,
		run.ID, run.SessionID, run.BackendInstanceID, run.NativeSessionID, run.ResumeStatus,
	).Scan(&run.CreatedAt, &run.UpdatedAt)
	return classify(err, "create run")
}

// GetRun returns a Run the user can access through its Project.
func (s *Store) GetRun(ctx context.Context, ownerID domain.UserID, id domain.RunID) (domain.Run, error) {
	return scanRun(s.q.QueryRow(ctx, `
		SELECT `+runColumns+`
		FROM runs r
		JOIN sessions s ON s.id = r.session_id
		JOIN projects p ON p.id = s.project_id
		WHERE r.id = $1 AND p.owner_id = $2`, id, ownerID))
}

// LatestRun returns the most recent Run of a Session. The current Run is where a
// new message continues, so that the provider native session is reused.
func (s *Store) LatestRun(ctx context.Context, sessionID domain.SessionID) (domain.Run, error) {
	return scanRun(s.q.QueryRow(ctx, `
		SELECT `+runColumns+`
		FROM runs r
		WHERE r.session_id = $1
		ORDER BY r.created_at DESC
		LIMIT 1`, sessionID))
}

// ListRuns returns every Run of a Session, oldest first. The Session timeline is
// continuous across them.
func (s *Store) ListRuns(ctx context.Context, sessionID domain.SessionID) ([]domain.Run, error) {
	rows, err := s.q.Query(ctx, `SELECT `+runColumns+` FROM runs r WHERE r.session_id = $1 ORDER BY r.created_at`, sessionID)
	if err != nil {
		return nil, classify(err, "list runs")
	}
	defer rows.Close()

	var out []domain.Run
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, classify(rows.Err(), "list runs")
}

// BindNativeSession records the provider native session a backend reported for a
// Run, and marks it resumable.
func (s *Store) BindNativeSession(ctx context.Context, id domain.RunID, nativeSessionID string) error {
	_, err := s.q.Exec(ctx, `
		UPDATE runs
		SET native_session_id = $2, resume_status = 'AVAILABLE', resume_reason = NULL, updated_at = now()
		WHERE id = $1`, id, nativeSessionID)
	return classify(err, "bind native session")
}

// SetResumeStatus records what the backend observed at actual resume time. A
// native session disappearing never deletes Core history.
func (s *Store) SetResumeStatus(ctx context.Context, id domain.RunID, status domain.ResumeStatus, reason *string) error {
	_, err := s.q.Exec(ctx, `
		UPDATE runs SET resume_status = $2, resume_reason = $3, updated_at = now() WHERE id = $1`,
		id, status, reason)
	return classify(err, "set resume status")
}

func scanRun(row scanner) (domain.Run, error) {
	var run domain.Run
	err := row.Scan(&run.ID, &run.SessionID, &run.BackendInstanceID, &run.NativeSessionID,
		&run.ResumeStatus, &run.ResumeReason, &run.CreatedAt, &run.UpdatedAt)
	return run, classify(err, "read run")
}
