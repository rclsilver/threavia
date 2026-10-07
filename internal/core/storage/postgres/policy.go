package postgres

import (
	"context"
	"encoding/json"

	"github.com/rclsilver/threavia/internal/core/domain"
)

// SetSessionExecutionPolicy stores the default policy of a Session.
func (s *Store) SetSessionExecutionPolicy(ctx context.Context, ownerID domain.UserID, id domain.SessionID, policy *domain.ExecutionPolicy) error {
	encoded, err := encodePolicy(policy)
	if err != nil {
		return err
	}
	tag, err := s.q.Exec(ctx, `
		UPDATE sessions s SET execution_policy = $3, updated_at = now()
		FROM projects p
		WHERE s.project_id = p.id AND s.id = $1 AND p.owner_id = $2`, id, ownerID, encoded)
	if err != nil {
		return classify(err, "set session execution policy")
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetJobExecutionPolicy stores the override of a single Job.
func (s *Store) SetJobExecutionPolicy(ctx context.Context, id domain.JobID, policy *domain.ExecutionPolicy) error {
	encoded, err := encodePolicy(policy)
	if err != nil {
		return err
	}
	_, err = s.q.Exec(ctx,
		`UPDATE jobs SET execution_policy = $2, updated_at = now() WHERE id = $1`, id, encoded)
	return classify(err, "set job execution policy")
}

// EffectivePolicy returns the policy that applies to a Job: its own override if
// it has one, otherwise its Session default, otherwise the restrained default.
func (s *Store) EffectivePolicy(ctx context.Context, jobID domain.JobID) (domain.ExecutionPolicy, error) {
	var sessionRaw, jobRaw []byte
	err := s.q.QueryRow(ctx, `
		SELECT sess.execution_policy, j.execution_policy
		FROM jobs j
		JOIN runs r ON r.id = j.run_id
		JOIN sessions sess ON sess.id = r.session_id
		WHERE j.id = $1`, jobID).Scan(&sessionRaw, &jobRaw)
	if err != nil {
		return domain.ExecutionPolicy{}, classify(err, "read execution policy")
	}

	sessionPolicy, err := decodePolicy(sessionRaw)
	if err != nil {
		return domain.ExecutionPolicy{}, err
	}
	jobPolicy, err := decodePolicy(jobRaw)
	if err != nil {
		return domain.ExecutionPolicy{}, err
	}
	return domain.Effective(sessionPolicy, jobPolicy), nil
}

// SessionPolicy returns the stored default of a Session, if it has one.
func (s *Store) SessionPolicy(ctx context.Context, id domain.SessionID) (*domain.ExecutionPolicy, error) {
	var raw []byte
	if err := s.q.QueryRow(ctx,
		`SELECT execution_policy FROM sessions WHERE id = $1`, id).Scan(&raw); err != nil {
		return nil, classify(err, "read session policy")
	}
	return decodePolicy(raw)
}

func encodePolicy(policy *domain.ExecutionPolicy) ([]byte, error) {
	if policy == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(policy)
	if err != nil {
		return nil, classify(err, "encode execution policy")
	}
	return encoded, nil
}

func decodePolicy(raw []byte) (*domain.ExecutionPolicy, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var policy domain.ExecutionPolicy
	if err := json.Unmarshal(raw, &policy); err != nil {
		return nil, classify(err, "decode execution policy")
	}
	return &policy, nil
}
