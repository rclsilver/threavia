package postgres

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/rclsilver/threavia/internal/core/domain"
)

func (s *Store) InstancePolicy(ctx context.Context, ownerID domain.UserID) (*domain.ExecutionPolicy, error) {
	var raw []byte
	err := s.q.QueryRow(ctx, `SELECT execution_policy FROM instance_execution_policies WHERE owner_id = $1`, ownerID).Scan(&raw)
	if errors.Is(classify(err, "read instance policy"), ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, classify(err, "read instance policy")
	}
	return decodePolicy(raw)
}

func (s *Store) SetInstanceExecutionPolicy(ctx context.Context, ownerID domain.UserID, policy *domain.ExecutionPolicy) error {
	if policy == nil {
		_, err := s.q.Exec(ctx, `DELETE FROM instance_execution_policies WHERE owner_id = $1`, ownerID)
		return classify(err, "reset instance policy")
	}
	raw, err := encodePolicy(policy)
	if err != nil {
		return err
	}
	_, err = s.q.Exec(ctx, `INSERT INTO instance_execution_policies (owner_id, execution_policy) VALUES ($1, $2)
		ON CONFLICT (owner_id) DO UPDATE SET execution_policy = EXCLUDED.execution_policy, updated_at = now()`, ownerID, raw)
	return classify(err, "set instance policy")
}

func (s *Store) BackendPolicy(ctx context.Context, ownerID domain.UserID, id domain.BackendInstanceID) (*domain.ExecutionPolicy, error) {
	var raw []byte
	if err := s.q.QueryRow(ctx, `SELECT execution_policy FROM backend_instances WHERE id = $1 AND owner_id = $2`, id, ownerID).Scan(&raw); err != nil {
		return nil, classify(err, "read backend policy")
	}
	return decodePolicy(raw)
}

func (s *Store) SetBackendExecutionPolicy(ctx context.Context, ownerID domain.UserID, id domain.BackendInstanceID, policy *domain.ExecutionPolicy) error {
	raw, err := encodePolicy(policy)
	if err != nil {
		return err
	}
	tag, err := s.q.Exec(ctx, `UPDATE backend_instances SET execution_policy = $3, updated_at = now() WHERE id = $1 AND owner_id = $2`, id, ownerID, raw)
	if err != nil {
		return classify(err, "set backend policy")
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

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

// SetProjectExecutionPolicy stores the default every Session of a Project
// inherits.
func (s *Store) SetProjectExecutionPolicy(ctx context.Context, ownerID domain.UserID, id domain.ProjectID, policy *domain.ExecutionPolicy) error {
	encoded, err := encodePolicy(policy)
	if err != nil {
		return err
	}
	tag, err := s.q.Exec(ctx, `
		UPDATE projects SET execution_policy = $3, updated_at = now()
		WHERE id = $1 AND owner_id = $2`, id, ownerID, encoded)
	if err != nil {
		return classify(err, "set project execution policy")
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ProjectPolicy returns the stored default of a Project, if it has one.
func (s *Store) ProjectPolicy(ctx context.Context, id domain.ProjectID) (*domain.ExecutionPolicy, error) {
	var raw []byte
	if err := s.q.QueryRow(ctx,
		`SELECT execution_policy FROM projects WHERE id = $1`, id).Scan(&raw); err != nil {
		return nil, classify(err, "read project policy")
	}
	return decodePolicy(raw)
}

// ProjectPolicyOfSession returns the instance/backend/project fallback for the
// latest Run. A dispatched Job instead uses its own Run's backend.
func (s *Store) ProjectPolicyOfSession(ctx context.Context, id domain.SessionID) (*domain.ExecutionPolicy, error) {
	var instanceRaw, backendRaw, projectRaw []byte
	if err := s.q.QueryRow(ctx, `
		SELECT i.execution_policy, b.execution_policy, p.execution_policy
		FROM sessions s JOIN projects p ON p.id = s.project_id
		LEFT JOIN LATERAL (SELECT backend_instance_id FROM runs WHERE session_id = s.id ORDER BY created_at DESC, id DESC LIMIT 1) r ON true
		LEFT JOIN backend_instances b ON b.id = r.backend_instance_id
		LEFT JOIN instance_execution_policies i ON i.owner_id = p.owner_id
		WHERE s.id = $1`, id).Scan(&instanceRaw, &backendRaw, &projectRaw); err != nil {
		return nil, classify(err, "read the project policy of a session")
	}
	policy, err := effectiveRaw(instanceRaw, backendRaw, projectRaw)
	return &policy, err
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

// EffectivePolicy merges instance, the actual Run's backend, project, session
// and job settings. Rules from every level remain in force.
func (s *Store) EffectivePolicy(ctx context.Context, jobID domain.JobID) (domain.ExecutionPolicy, error) {
	var instanceRaw, backendRaw, projectRaw, sessionRaw, jobRaw []byte
	err := s.q.QueryRow(ctx, `
		SELECT i.execution_policy, b.execution_policy, p.execution_policy, sess.execution_policy, j.execution_policy
		FROM jobs j
		JOIN runs r ON r.id = j.run_id
		JOIN sessions sess ON sess.id = r.session_id
		JOIN projects p ON p.id = sess.project_id
		JOIN backend_instances b ON b.id = r.backend_instance_id
		LEFT JOIN instance_execution_policies i ON i.owner_id = p.owner_id
		WHERE j.id = $1`, jobID).Scan(&instanceRaw, &backendRaw, &projectRaw, &sessionRaw, &jobRaw)
	if err != nil {
		return domain.ExecutionPolicy{}, classify(err, "read execution policy")
	}

	return effectiveRaw(instanceRaw, backendRaw, projectRaw, sessionRaw, jobRaw)
}

func effectiveRaw(raws ...[]byte) (domain.ExecutionPolicy, error) {
	levels := make([]*domain.ExecutionPolicy, 0, len(raws))
	for _, raw := range raws {
		level, err := decodePolicy(raw)
		if err != nil {
			return domain.ExecutionPolicy{}, err
		}
		levels = append(levels, level)
	}
	return domain.Effective(levels...), nil
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
