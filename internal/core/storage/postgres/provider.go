package postgres

import (
	"context"
	"encoding/json"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/events"
	"time"
)

func (s *Store) SetJobBackendWait(ctx context.Context, id domain.JobID, wait *domain.BackendWait, activeSeconds uint32) error {
	encoded, err := json.Marshal(wait)
	if err != nil {
		return err
	}
	_, err = s.q.Exec(ctx, `UPDATE jobs SET backend_wait=$2,
	 provider_active_seconds=provider_active_seconds+CASE WHEN $3::bigint>0 THEN $3::bigint WHEN provider_attempt_started_at IS NULL THEN 0
	 ELSE greatest(0,extract(epoch FROM clock_timestamp()-provider_attempt_started_at)) END,
	 provider_attempt_started_at=NULL WHERE id=$1 AND status='WAITING_BACKEND'`, id, encoded, activeSeconds)
	return classify(err, "record provider wait")
}

func (s *Store) StartProviderAttempt(ctx context.Context, id domain.JobID) error {
	_, err := s.q.Exec(ctx, `UPDATE jobs SET provider_attempt_started_at=clock_timestamp() WHERE id=$1`, id)
	return classify(err, "start provider attempt")
}

func (s *Store) JobProviderActiveSeconds(ctx context.Context, id domain.JobID) (uint32, error) {
	var seconds uint32
	err := s.q.QueryRow(ctx, `SELECT least(4294967295,ceil(provider_active_seconds))::bigint FROM jobs WHERE id=$1`, id).Scan(&seconds)
	return seconds, classify(err, "read provider attempt duration")
}

// ProviderRecoveryJobs includes unstarted work held behind a provider condition.
// It excludes connection-loss waits: those may still be executing on a laptop.
// It skips a queued Job whose Run already holds an active Job: that Job cannot
// take the slot, and trying only trips jobs_run_single_active_key. The active
// Job starts the next one when it ends.
func (s *Store) ProviderRecoveryJobs(ctx context.Context, at time.Time) ([]domain.JobID, error) {
	rows, err := s.q.Query(ctx, `SELECT id FROM jobs j
		WHERE (status='QUEUED' AND NOT EXISTS (SELECT 1 FROM jobs a WHERE a.run_id=j.run_id
		 AND a.status IN ('RUNNING','WAITING_INPUT','WAITING_VALIDATION','WAITING_BACKEND','CANCELLING')))
		 OR (status='WAITING_BACKEND' AND backend_wait IS NOT NULL
		 AND (backend_wait->>'retryAt' IS NULL OR (backend_wait->>'retryAt')::timestamptz <= $1))
		ORDER BY created_at`, at)
	if err != nil {
		return nil, classify(err, "read provider recovery jobs")
	}
	defer rows.Close()
	var ids []domain.JobID
	for rows.Next() {
		var id domain.JobID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// The complete relevant history, without a display pagination limit. Recovery
// must preserve later steering and every earlier attempt's resource budget.
func (s *Store) JobProviderHistory(ctx context.Context, id domain.JobID) ([]events.Envelope, error) {
	rows, err := s.q.Query(ctx, `SELECT `+eventColumns+` FROM events e WHERE job_id=$1
	 AND type IN ('job.started','job.waiting_backend','tool.started','user.message') ORDER BY global_sequence`, id)
	if err != nil {
		return nil, classify(err, "read provider attempt history")
	}
	defer rows.Close()
	var out []events.Envelope
	for rows.Next() {
		e, err := scanEnvelope(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
