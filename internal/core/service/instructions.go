package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/events"
	"github.com/rclsilver/threavia/internal/core/storage/postgres"
)

// InstructionsView is what an editor of one level needs: what this level
// says, and what the levels above it already say, assembled as a Job would
// read them.
//
// Inherited is only the global level today: a project and a backend never sit
// above each other, since which backend runs a Session is known per Job, not
// per Project.
type InstructionsView struct {
	Own       domain.Instructions `json:"own"`
	Inherited domain.Instructions `json:"inherited"`
}

// errSupervisionAboveSession refuses a default policy carrying supervision.
// Supervision is written with the instructions now, where each level adds to
// the others; a policy replaces the one above it wholesale, which is how a
// project that only changed the mode lost the global supervision. An old
// client that still sends it has to hear where it went rather than see it
// silently dropped.
var errSupervisionAboveSession = fmt.Errorf("%w: supervision is set with the instructions now: use /instructions", ErrInvalid)

// checkDefaultPolicy validates a policy set at the instance, backend or
// project level, where only the session and job levels may carry supervision.
// A blank statement is what an old client sends when nothing was written, so
// it is cleared rather than refused.
func checkDefaultPolicy(policy *domain.ExecutionPolicy) error {
	if policy == nil {
		return nil
	}
	if strings.TrimSpace(policy.Supervision) != "" {
		return errSupervisionAboveSession
	}
	policy.Supervision = ""
	if err := policy.Validate(); err != nil {
		return fmt.Errorf("%w: %s", ErrInvalid, err)
	}
	return nil
}

// trimInstructions drops the surrounding whitespace of both texts, as the
// project form always did for the project agent text.
func trimInstructions(in domain.Instructions) domain.Instructions {
	return domain.Instructions{Agent: strings.TrimSpace(in.Agent), Supervision: strings.TrimSpace(in.Supervision)}
}

// inheritedInstructions renders the global level as a project or backend
// editor shows it above its own fields.
func (s *Service) inheritedInstructions(ctx context.Context, identity auth.Identity) (domain.Instructions, error) {
	global, err := s.store.InstanceInstructions(ctx, identity.UserID)
	if err != nil {
		return domain.Instructions{}, translate(err)
	}
	return domain.Instructions{
		Agent:       domain.Layered(domain.LayeredPreamble, []domain.Layer{{Label: "Global", Text: global.Agent}}),
		Supervision: domain.Layered(domain.LayeredSupervisionPreamble, []domain.Layer{{Label: "Global", Text: global.Supervision}}),
	}, nil
}

// replaceInstructions writes the texts of the global or a backend level and,
// when one of them changed, announces it as instructions.updated with the
// level in the payload: an editor open in another client is otherwise left
// showing, and saving back, the old texts. It returns the names of the fields
// that changed, none for a PUT that repeats what is stored.
func (s *Service) replaceInstructions(ctx context.Context, identity auth.Identity, in domain.Instructions, level map[string]any,
	read func(*postgres.Store) (domain.Instructions, error), write func(*postgres.Store) error) ([]string, error) {
	var fields []string
	b := &batch{ownerID: identity.UserID}
	err := s.store.WithTx(ctx, func(tx *postgres.Store) error {
		current, err := read(tx)
		if err != nil {
			return err
		}
		if in.Agent != current.Agent {
			fields = append(fields, "agent")
		}
		if in.Supervision != current.Supervision {
			fields = append(fields, "supervision")
		}
		if len(fields) == 0 {
			return nil
		}
		if err := write(tx); err != nil {
			return err
		}
		return s.appendAll(ctx, tx, b, record{eventType: events.TypeInstructionsUpdated, payload: level})
	})
	if err != nil {
		return nil, translate(err)
	}
	s.flush(b)
	return fields, nil
}

// InstanceInstructions returns the caller's global instructions.
func (s *Service) InstanceInstructions(ctx context.Context, identity auth.Identity) (InstructionsView, error) {
	own, err := s.store.InstanceInstructions(ctx, identity.UserID)
	return InstructionsView{Own: own}, translate(err)
}

// SetInstanceInstructions replaces the caller's global instructions. Like
// every instructions change, it reaches the next Job: no running Job is told.
// Clients are, so an editor open elsewhere can refresh.
func (s *Service) SetInstanceInstructions(ctx context.Context, identity auth.Identity, in domain.Instructions) (InstructionsView, error) {
	in = trimInstructions(in)
	if err := in.Validate(); err != nil {
		return InstructionsView{}, fmt.Errorf("%w: %s", ErrInvalid, err)
	}
	_, err := s.replaceInstructions(ctx, identity, in, map[string]any{"level": "instance"},
		func(tx *postgres.Store) (domain.Instructions, error) {
			return tx.InstanceInstructions(ctx, identity.UserID)
		},
		func(tx *postgres.Store) error { return tx.SetInstanceInstructions(ctx, identity.UserID, in) })
	if err != nil {
		return InstructionsView{}, err
	}
	s.audit(ctx, identity, postgres.AuditEntry{Action: "instructions.instance_set", SubjectID: string(identity.UserID), Detail: mustJSON(in)})
	return s.InstanceInstructions(ctx, identity)
}

// ProjectInstructions returns the instructions of a Project and the global
// ones above them.
func (s *Service) ProjectInstructions(ctx context.Context, identity auth.Identity, id domain.ProjectID) (InstructionsView, error) {
	own, err := s.store.ProjectInstructionsOf(ctx, identity.UserID, id)
	if err != nil {
		return InstructionsView{}, translate(err)
	}
	inherited, err := s.inheritedInstructions(ctx, identity)
	return InstructionsView{Own: own, Inherited: inherited}, err
}

// SetProjectInstructions replaces the instruction texts of a Project. A nil
// agent keeps the stored agent text. It is read under the project lock, so a
// caller that changes only the supervision cannot write back an agent text
// someone else replaced in the meantime.
//
// The agent text is projects.instructions, the one the project form edits too,
// so a change to it is announced as project.updated like a change made there:
// clients refresh their copy of the Project from that event, and a form left
// open on the old text would otherwise PATCH it back. The supervision is
// announced the same way so an open instructions editor hears of it.
//
// The agent text has no length limit, as through the project form: it predates
// the limit and may already be longer. Only the supervision is bounded.
func (s *Service) SetProjectInstructions(ctx context.Context, identity auth.Identity, id domain.ProjectID, agent *string, supervision string) (InstructionsView, error) {
	in := domain.Instructions{Supervision: strings.TrimSpace(supervision)}
	if err := in.Validate(); err != nil {
		return InstructionsView{}, fmt.Errorf("%w: %s", ErrInvalid, err)
	}
	b := &batch{ownerID: identity.UserID}
	err := s.store.WithTx(ctx, func(tx *postgres.Store) error {
		project, err := tx.LockProject(ctx, identity.UserID, id)
		if err != nil {
			return err
		}
		current, err := tx.ProjectInstructionsOf(ctx, identity.UserID, id)
		if err != nil {
			return err
		}
		in.Agent = current.Agent
		if agent != nil {
			in.Agent = strings.TrimSpace(*agent)
		}
		var fields []string
		if in.Agent != current.Agent {
			fields = append(fields, "instructions")
		}
		if in.Supervision != current.Supervision {
			fields = append(fields, "supervisionInstructions")
		}
		if len(fields) == 0 {
			return nil
		}
		if err := tx.SetProjectInstructions(ctx, identity.UserID, id, in); err != nil {
			return err
		}
		payload := projectAuditPayload(project)
		payload.ChangedFields = fields
		return s.recordAuditedChange(ctx, tx, b, identity, events.TypeProjectUpdated, domain.Scope{ProjectID: id}, string(id), payload)
	})
	if err != nil {
		return InstructionsView{}, translate(err)
	}
	s.flush(b)
	s.audit(ctx, identity, postgres.AuditEntry{Action: "instructions.project_set", ProjectID: string(id), SubjectID: string(id), Detail: mustJSON(in)})
	return s.ProjectInstructions(ctx, identity, id)
}

// BackendInstructions returns the instructions of a backend and the global
// ones above them.
func (s *Service) BackendInstructions(ctx context.Context, identity auth.Identity, id domain.BackendInstanceID) (InstructionsView, error) {
	own, err := s.store.BackendInstructions(ctx, identity.UserID, id)
	if err != nil {
		return InstructionsView{}, translate(err)
	}
	inherited, err := s.inheritedInstructions(ctx, identity)
	return InstructionsView{Own: own, Inherited: inherited}, err
}

// SetBackendInstructions replaces both instruction texts of a backend, and
// announces a change as SetInstanceInstructions does.
func (s *Service) SetBackendInstructions(ctx context.Context, identity auth.Identity, id domain.BackendInstanceID, in domain.Instructions) (InstructionsView, error) {
	in = trimInstructions(in)
	if err := in.Validate(); err != nil {
		return InstructionsView{}, fmt.Errorf("%w: %s", ErrInvalid, err)
	}
	_, err := s.replaceInstructions(ctx, identity, in, map[string]any{"level": "backend", "backendId": id},
		func(tx *postgres.Store) (domain.Instructions, error) {
			return tx.BackendInstructions(ctx, identity.UserID, id)
		},
		func(tx *postgres.Store) error { return tx.SetBackendInstructions(ctx, identity.UserID, id, in) })
	if err != nil {
		return InstructionsView{}, err
	}
	s.audit(ctx, identity, postgres.AuditEntry{Action: "instructions.backend_set", SubjectID: string(id), Detail: mustJSON(in)})
	return s.BackendInstructions(ctx, identity, id)
}

// assembleInstructions orders and labels the layers of a Job. It is the only
// place that does, so a preview and the Job it previews cannot drift apart.
//
// The agent has no session or job level: what changes per Session or Job is
// added after this text by Core, as the notes of projectContext.
func assembleInstructions(l postgres.JobLayers) domain.Instructions {
	return domain.Instructions{
		Agent: domain.Layered(domain.LayeredPreamble, []domain.Layer{
			{Label: "Global", Text: l.Global.Agent},
			{Label: "Project " + l.ProjectName, Text: l.Project.Agent},
			{Label: "Backend " + l.BackendName, Text: l.Backend.Agent},
		}),
		Supervision: domain.Layered(domain.LayeredSupervisionPreamble, []domain.Layer{
			{Label: "Global", Text: l.Global.Supervision},
			{Label: "Project " + l.ProjectName, Text: l.Project.Supervision},
			{Label: "Backend " + l.BackendName, Text: l.Backend.Supervision},
			{Label: "Session", Text: l.SessionSupervision},
			{Label: "Job", Text: l.JobSupervision},
		}),
	}
}

// jobInstructions returns the assembled instructions of a Job, as they are
// right now: a change reaches the next Job, or a running one when its policy
// is pushed again.
func (s *Service) jobInstructions(ctx context.Context, jobID domain.JobID) (domain.Instructions, error) {
	layers, err := s.store.JobInstructionLayers(ctx, jobID)
	if err != nil {
		return domain.Instructions{}, err
	}
	return assembleInstructions(layers), nil
}

// PreviewInstructions returns what a Job started in a Project on a backend
// would read, and in an existing Session of that Project when one is given.
// The agent text is the one Core opens the system prompt with, before the
// notes it adds for a particular Job.
func (s *Service) PreviewInstructions(ctx context.Context, identity auth.Identity, projectID domain.ProjectID, backendID domain.BackendInstanceID, sessionID *domain.SessionID) (domain.Instructions, error) {
	// An identifier the database cannot even parse names nothing, rather than
	// failing the query.
	if !isUUID(string(projectID)) || !isUUID(string(backendID)) || (sessionID != nil && !isUUID(string(*sessionID))) {
		return domain.Instructions{}, ErrNotFound
	}
	layers, err := s.store.PreviewInstructionLayers(ctx, identity.UserID, projectID, backendID, sessionID)
	if err != nil {
		return domain.Instructions{}, translate(err)
	}
	return assembleInstructions(layers), nil
}
