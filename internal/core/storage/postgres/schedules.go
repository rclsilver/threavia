package postgres

import (
	"context"
	"time"

	"github.com/rclsilver/threavia/internal/core/domain"
)

const scheduleColumns = `sc.id, sc.session_id, sess.project_id, p.owner_id, sc.cron, sc.timezone,
	sc.message, sc.enabled, sc.next_run_at, sc.last_run_at, sc.last_outcome, sc.last_job_id,
	sc.created_at, sc.updated_at`

const scheduleFrom = `FROM schedules sc
	JOIN sessions sess ON sess.id = sc.session_id
	JOIN projects p ON p.id = sess.project_id`

// CreateSchedule inserts a Schedule.
func (s *Store) CreateSchedule(ctx context.Context, schedule *domain.Schedule) error {
	err := s.q.QueryRow(ctx, `
		INSERT INTO schedules (id, session_id, cron, timezone, message, enabled, next_run_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING created_at, updated_at`,
		schedule.ID, schedule.SessionID, schedule.Cron, schedule.Timezone, schedule.Message,
		schedule.Enabled, schedule.NextRunAt,
	).Scan(&schedule.CreatedAt, &schedule.UpdatedAt)
	return classify(err, "create schedule")
}

// ListSchedules returns the Schedules of a Session the user can access.
func (s *Store) ListSchedules(ctx context.Context, ownerID domain.UserID, sessionID domain.SessionID) ([]domain.Schedule, error) {
	rows, err := s.q.Query(ctx, `SELECT `+scheduleColumns+` `+scheduleFrom+`
		WHERE sc.session_id = $1 AND p.owner_id = $2
		ORDER BY sc.created_at`, sessionID, ownerID)
	if err != nil {
		return nil, classify(err, "list schedules")
	}
	defer rows.Close()
	var out []domain.Schedule
	for rows.Next() {
		schedule, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, schedule)
	}
	return out, classify(rows.Err(), "list schedules")
}

// GetSchedule returns a Schedule the user can access.
func (s *Store) GetSchedule(ctx context.Context, ownerID domain.UserID, id domain.ScheduleID) (domain.Schedule, error) {
	return scanSchedule(s.q.QueryRow(ctx, `SELECT `+scheduleColumns+` `+scheduleFrom+`
		WHERE sc.id = $1 AND p.owner_id = $2`, id, ownerID))
}

// UpdateSchedule replaces what a person can change on a Schedule.
func (s *Store) UpdateSchedule(ctx context.Context, schedule *domain.Schedule) error {
	tag, err := s.q.Exec(ctx, `
		UPDATE schedules SET cron = $2, timezone = $3, message = $4, enabled = $5,
		       next_run_at = $6, updated_at = now()
		WHERE id = $1`,
		schedule.ID, schedule.Cron, schedule.Timezone, schedule.Message, schedule.Enabled, schedule.NextRunAt)
	if err != nil {
		return classify(err, "update schedule")
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteSchedule removes a Schedule the user can access.
func (s *Store) DeleteSchedule(ctx context.Context, ownerID domain.UserID, id domain.ScheduleID) error {
	tag, err := s.q.Exec(ctx, `
		DELETE FROM schedules sc
		USING sessions sess, projects p
		WHERE sc.id = $1 AND sess.id = sc.session_id AND p.id = sess.project_id AND p.owner_id = $2`,
		id, ownerID)
	if err != nil {
		return classify(err, "delete schedule")
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DueSchedules returns the enabled Schedules whose next run has come, across
// every user: the scheduler is Core itself.
func (s *Store) DueSchedules(ctx context.Context, now time.Time, limit int) ([]domain.Schedule, error) {
	rows, err := s.q.Query(ctx, `SELECT `+scheduleColumns+` `+scheduleFrom+`
		WHERE sc.enabled AND sc.next_run_at <= $1
		ORDER BY sc.next_run_at
		LIMIT $2`, now, limit)
	if err != nil {
		return nil, classify(err, "list due schedules")
	}
	defer rows.Close()
	var out []domain.Schedule
	for rows.Next() {
		schedule, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, schedule)
	}
	return out, classify(rows.Err(), "list due schedules")
}

// ClaimScheduleRun moves a due Schedule to its next run, only if it is still
// due for the run that was read. The one caller that moves it owns that run;
// every other one finds nothing to update and leaves it alone.
func (s *Store) ClaimScheduleRun(ctx context.Context, id domain.ScheduleID, due time.Time, next *time.Time) (bool, error) {
	tag, err := s.q.Exec(ctx, `
		UPDATE schedules SET next_run_at = $3, updated_at = now()
		WHERE id = $1 AND enabled AND next_run_at = $2`, id, due, next)
	if err != nil {
		return false, classify(err, "claim schedule run")
	}
	return tag.RowsAffected() == 1, nil
}

// RecordScheduleRun says what became of a run.
func (s *Store) RecordScheduleRun(ctx context.Context, id domain.ScheduleID, at time.Time, outcome domain.ScheduleOutcome, jobID *domain.JobID) error {
	_, err := s.q.Exec(ctx, `
		UPDATE schedules SET last_run_at = $2, last_outcome = $3, last_job_id = $4, updated_at = now()
		WHERE id = $1`, id, at, outcome, jobID)
	return classify(err, "record schedule run")
}

func scanSchedule(row scanner) (domain.Schedule, error) {
	var schedule domain.Schedule
	err := row.Scan(&schedule.ID, &schedule.SessionID, &schedule.ProjectID, &schedule.OwnerID,
		&schedule.Cron, &schedule.Timezone, &schedule.Message, &schedule.Enabled,
		&schedule.NextRunAt, &schedule.LastRunAt, &schedule.LastOutcome, &schedule.LastJobID,
		&schedule.CreatedAt, &schedule.UpdatedAt)
	return schedule, classify(err, "read schedule")
}
