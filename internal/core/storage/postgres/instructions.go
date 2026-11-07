package postgres

import (
	"context"
	"errors"

	"github.com/rclsilver/threavia/internal/core/domain"
)

// InstanceInstructions returns the global instructions of an owner. An owner
// who never wrote any reads empty texts rather than an error: the global level
// always exists.
func (s *Store) InstanceInstructions(ctx context.Context, ownerID domain.UserID) (domain.Instructions, error) {
	var in domain.Instructions
	err := s.q.QueryRow(ctx,
		`SELECT agent, supervision FROM instance_instructions WHERE owner_id = $1`, ownerID).
		Scan(&in.Agent, &in.Supervision)
	if err = classify(err, "read instance instructions"); errors.Is(err, ErrNotFound) {
		return domain.Instructions{}, nil
	}
	return in, err
}

// SetInstanceInstructions replaces the global instructions of an owner.
func (s *Store) SetInstanceInstructions(ctx context.Context, ownerID domain.UserID, in domain.Instructions) error {
	_, err := s.q.Exec(ctx, `
		INSERT INTO instance_instructions (owner_id, agent, supervision) VALUES ($1, $2, $3)
		ON CONFLICT (owner_id) DO UPDATE
		SET agent = EXCLUDED.agent, supervision = EXCLUDED.supervision, updated_at = now()`,
		ownerID, in.Agent, in.Supervision)
	return classify(err, "set instance instructions")
}

// ProjectInstructionsOf returns the instructions of a Project. The agent text
// is projects.instructions, the one the project form has always edited.
func (s *Store) ProjectInstructionsOf(ctx context.Context, ownerID domain.UserID, id domain.ProjectID) (domain.Instructions, error) {
	var in domain.Instructions
	err := s.q.QueryRow(ctx,
		`SELECT instructions, supervision_instructions FROM projects WHERE id = $1 AND owner_id = $2`, id, ownerID).
		Scan(&in.Agent, &in.Supervision)
	return in, classify(err, "read project instructions")
}

// SetProjectInstructions replaces both instruction texts of a Project.
func (s *Store) SetProjectInstructions(ctx context.Context, ownerID domain.UserID, id domain.ProjectID, in domain.Instructions) error {
	tag, err := s.q.Exec(ctx, `
		UPDATE projects SET instructions = $3, supervision_instructions = $4, updated_at = now()
		WHERE id = $1 AND owner_id = $2`, id, ownerID, in.Agent, in.Supervision)
	if err != nil {
		return classify(err, "set project instructions")
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// BackendInstructions returns the instructions of a backend, which are its
// owner's.
func (s *Store) BackendInstructions(ctx context.Context, ownerID domain.UserID, id domain.BackendInstanceID) (domain.Instructions, error) {
	var in domain.Instructions
	err := s.q.QueryRow(ctx,
		`SELECT agent_instructions, supervision_instructions FROM backend_instances WHERE id = $1 AND owner_id = $2`, id, ownerID).
		Scan(&in.Agent, &in.Supervision)
	return in, classify(err, "read backend instructions")
}

// SetBackendInstructions replaces both instruction texts of a backend.
func (s *Store) SetBackendInstructions(ctx context.Context, ownerID domain.UserID, id domain.BackendInstanceID, in domain.Instructions) error {
	tag, err := s.q.Exec(ctx, `
		UPDATE backend_instances SET agent_instructions = $3, supervision_instructions = $4, updated_at = now()
		WHERE id = $1 AND owner_id = $2`, id, ownerID, in.Agent, in.Supervision)
	if err != nil {
		return classify(err, "set backend instructions")
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// JobLayers is every level of instructions that applies to one Job, with the
// names its labels need, before assembly.
type JobLayers struct {
	ProjectName, BackendName string
	Global, Project, Backend domain.Instructions
	// SessionSupervision and JobSupervision are the supervision of the Session
	// and Job policies themselves, never an inherited value: those two levels
	// have no agent text.
	SessionSupervision, JobSupervision string
}

// JobInstructionLayers reads the layers of a Job in one query. The backend is
// the one of the Job's own Run, as for EffectivePolicy, and the global level is
// the project owner's.
func (s *Store) JobInstructionLayers(ctx context.Context, jobID domain.JobID) (JobLayers, error) {
	var l JobLayers
	err := s.q.QueryRow(ctx, `
		SELECT p.name, b.name,
		       COALESCE(ii.agent, ''), COALESCE(ii.supervision, ''),
		       p.instructions, p.supervision_instructions,
		       b.agent_instructions, b.supervision_instructions,
		       COALESCE(sess.execution_policy->>'supervision', ''),
		       COALESCE(j.execution_policy->>'supervision', '')
		FROM jobs j
		JOIN runs r ON r.id = j.run_id
		JOIN sessions sess ON sess.id = r.session_id
		JOIN projects p ON p.id = sess.project_id
		JOIN backend_instances b ON b.id = r.backend_instance_id
		LEFT JOIN instance_instructions ii ON ii.owner_id = p.owner_id
		WHERE j.id = $1`, jobID).Scan(
		&l.ProjectName, &l.BackendName,
		&l.Global.Agent, &l.Global.Supervision,
		&l.Project.Agent, &l.Project.Supervision,
		&l.Backend.Agent, &l.Backend.Supervision,
		&l.SessionSupervision, &l.JobSupervision)
	if err != nil {
		return JobLayers{}, classify(err, "read the instruction layers of a job")
	}
	return l, nil
}
