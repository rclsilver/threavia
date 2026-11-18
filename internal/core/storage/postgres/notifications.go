package postgres

import (
	"context"
	"errors"

	"github.com/rclsilver/threavia/internal/core/domain"
)

const notificationColumns = `n.id, n.project_id, n.session_id, n.run_id, n.job_id, n.level,
	n.title, n.message, n.count, n.created_at, n.updated_at, n.read_at`

// CreateNotification records n for the Session it names. An identical unread
// notification of the same Session is not duplicated: its count goes up and it
// moves to the top again. created says whether a new row was made.
func (s *Store) CreateNotification(ctx context.Context, n *domain.Notification) (created bool, err error) {
	if n.ID == "" {
		n.ID = domain.NewNotificationID()
	}
	err = s.q.QueryRow(ctx, `
		INSERT INTO notifications (id, project_id, session_id, run_id, job_id, level, title, message)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (session_id, level, (md5(title || E'\n' || message))) WHERE read_at IS NULL
		DO UPDATE SET count = notifications.count + 1, updated_at = now()
		RETURNING id, count, created_at, updated_at, (xmax = 0)`,
		n.ID, n.Scope.ProjectID, n.Scope.SessionID, nullable(string(n.Scope.RunID)), nullable(string(n.Scope.JobID)),
		n.Level, n.Title, n.Message,
	).Scan(&n.ID, &n.Count, &n.CreatedAt, &n.UpdatedAt, &created)
	return created, classify(err, "create notification")
}

// nullable maps an empty identifier to NULL.
func nullable(id string) any {
	if id == "" {
		return nil
	}
	return id
}

// ListNotifications returns the person's notifications, WARNING first and the
// most recently raised first, for one Session or for all of them.
func (s *Store) ListNotifications(ctx context.Context, ownerID domain.UserID, unreadOnly bool, sessionID domain.SessionID) ([]domain.Notification, error) {
	rows, err := s.q.Query(ctx, `
		SELECT `+notificationColumns+`
		FROM notifications n JOIN projects p ON p.id = n.project_id
		WHERE p.owner_id = $1
		  AND (NOT $2 OR n.read_at IS NULL)
		  AND ($3 = '' OR n.session_id::text = $3)
		ORDER BY (n.level = 'WARNING') DESC, n.updated_at DESC`, ownerID, unreadOnly, string(sessionID))
	if err != nil {
		return nil, classify(err, "list notifications")
	}
	defer rows.Close()
	return collect(rows, scanNotification, "list notifications")
}

// MarkNotificationRead marks one notification read. changed is false when it
// already was, so a second device reading it again announces nothing.
func (s *Store) MarkNotificationRead(ctx context.Context, ownerID domain.UserID, id domain.NotificationID) (n domain.Notification, changed bool, err error) {
	n, err = scanNotification(s.q.QueryRow(ctx, `
		UPDATE notifications n SET read_at = now()
		FROM projects p
		WHERE n.id = $1 AND p.id = n.project_id AND p.owner_id = $2 AND n.read_at IS NULL
		RETURNING `+notificationColumns, id, ownerID))
	if err == nil {
		return n, true, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return n, false, err
	}
	// Nothing moved: already read, or not this person's notification.
	n, err = scanNotification(s.q.QueryRow(ctx, `
		SELECT `+notificationColumns+`
		FROM notifications n JOIN projects p ON p.id = n.project_id
		WHERE n.id = $1 AND p.owner_id = $2`, id, ownerID))
	return n, false, err
}

// MarkNotificationsRead marks every unread notification read, of one Session
// when sessionID is set, and returns the ones that changed.
func (s *Store) MarkNotificationsRead(ctx context.Context, ownerID domain.UserID, sessionID domain.SessionID) ([]domain.Notification, error) {
	rows, err := s.q.Query(ctx, `
		UPDATE notifications n SET read_at = now()
		FROM projects p
		WHERE p.id = n.project_id AND p.owner_id = $1 AND n.read_at IS NULL
		  AND ($2 = '' OR n.session_id::text = $2)
		RETURNING `+notificationColumns, ownerID, string(sessionID))
	if err != nil {
		return nil, classify(err, "mark notifications read")
	}
	defer rows.Close()
	return collect(rows, scanNotification, "mark notifications read")
}

func scanNotification(row scanner) (domain.Notification, error) {
	var n domain.Notification
	var runID, jobID *string
	err := row.Scan(&n.ID, &n.Scope.ProjectID, &n.Scope.SessionID, &runID, &jobID, &n.Level,
		&n.Title, &n.Message, &n.Count, &n.CreatedAt, &n.UpdatedAt, &n.ReadAt)
	if runID != nil {
		n.Scope.RunID = domain.RunID(*runID)
	}
	if jobID != nil {
		n.Scope.JobID = domain.JobID(*jobID)
	}
	return n, classify(err, "read notification")
}
