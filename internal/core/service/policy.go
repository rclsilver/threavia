package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/storage/postgres"
)

// SetProjectExecutionPolicy sets the default every Session of a Project
// inherits.
//
// A Session that has set nothing of its own follows it from the next Job, and
// a Job already running gets it pushed, exactly as a Session change is.
func (s *Service) SetProjectExecutionPolicy(ctx context.Context, identity auth.Identity, projectID domain.ProjectID, policy *domain.ExecutionPolicy) (domain.ExecutionPolicy, error) {
	if _, err := s.store.GetProject(ctx, identity.UserID, projectID); err != nil {
		return domain.ExecutionPolicy{}, translate(err)
	}
	if policy != nil {
		if err := policy.Validate(); err != nil {
			return domain.ExecutionPolicy{}, fmt.Errorf("%w: %s", ErrInvalid, err)
		}
	}
	if err := s.store.SetProjectExecutionPolicy(ctx, identity.UserID, projectID, policy); err != nil {
		return domain.ExecutionPolicy{}, translate(err)
	}

	effective := domain.Effective(policy, nil, nil)
	s.audit(ctx, identity, postgres.AuditEntry{
		Action:    "execution_policy.set",
		ProjectID: string(projectID),
		SubjectID: string(projectID),
		Detail:    mustJSON(effective),
	})

	// Every Session that never overrode it is now running under a different
	// policy, so the Jobs in flight have to hear about it too.
	sessions, err := s.store.ListSessions(ctx, identity.UserID, projectID, true)
	if err != nil {
		s.logger.Error("cannot list the sessions to update their policy",
			slog.String("projectId", string(projectID)), slog.String("error", err.Error()))
		return effective, nil
	}
	for _, session := range sessions {
		s.pushPolicy(ctx, session)
	}
	return effective, nil
}

// ProjectExecutionPolicy returns the default a Project hands to its Sessions.
func (s *Service) ProjectExecutionPolicy(ctx context.Context, identity auth.Identity, projectID domain.ProjectID) (domain.ExecutionPolicy, error) {
	if _, err := s.store.GetProject(ctx, identity.UserID, projectID); err != nil {
		return domain.ExecutionPolicy{}, translate(err)
	}
	stored, err := s.store.ProjectPolicy(ctx, projectID)
	if err != nil {
		return domain.ExecutionPolicy{}, translate(err)
	}
	return domain.Effective(stored, nil, nil), nil
}

// SetSessionExecutionPolicy sets what the agent may do in a Session by default.
//
// A nil policy is how a Session goes back to following its Project.
func (s *Service) SetSessionExecutionPolicy(ctx context.Context, identity auth.Identity, sessionID domain.SessionID, policy *domain.ExecutionPolicy) (domain.ExecutionPolicy, error) {
	session, err := s.store.GetSession(ctx, identity.UserID, sessionID)
	if err != nil {
		return domain.ExecutionPolicy{}, translate(err)
	}
	if policy != nil {
		if err := policy.Validate(); err != nil {
			return domain.ExecutionPolicy{}, fmt.Errorf("%w: %s", ErrInvalid, err)
		}
	}
	project, err := s.store.ProjectPolicyOfSession(ctx, sessionID)
	if err != nil {
		return domain.ExecutionPolicy{}, translate(err)
	}
	// Refused here as well as dropped at merge time. The merge is what makes
	// the guarantee hold; saying so at the point someone writes the rule is
	// what keeps them from believing it took.
	if policy != nil {
		if blocked := contradicted(project, policy.Rules); blocked != nil {
			return domain.ExecutionPolicy{}, fmt.Errorf(
				"%w: the project refuses %s %q, and a session cannot allow it back",
				ErrInvalid, blocked.Capability, blocked.Match)
		}
	}
	if err := s.store.SetSessionExecutionPolicy(ctx, identity.UserID, sessionID, policy); err != nil {
		return domain.ExecutionPolicy{}, translate(err)
	}

	effective := domain.Effective(project, policy, nil)
	// Loosening what an agent may do is exactly the kind of act an audit trail
	// exists for.
	s.audit(ctx, identity, postgres.AuditEntry{
		Action:    "execution_policy.set",
		ProjectID: string(session.ProjectID),
		SessionID: string(sessionID),
		SubjectID: string(sessionID),
		Detail:    mustJSON(effective),
	})
	s.pushPolicy(ctx, session)
	return effective, nil
}

// pushPolicy hands a changed Session policy to the Job already running in it.
// The user changes the policy because of what that Job is doing, so making them
// wait for the next message would answer a question they did not ask.
//
// A Job with its own override keeps it: the Session default does not apply to
// it. A backend that is offline gets nothing, and the Job carries on with the
// policy it started with; that is the one case left to the next Job.
func (s *Service) pushPolicy(ctx context.Context, session domain.Session) {
	runs, err := s.store.ListRuns(ctx, session.ID)
	if err != nil {
		s.logger.Error("cannot list the runs to update their policy",
			slog.String("sessionId", string(session.ID)), slog.String("error", err.Error()))
		return
	}
	for _, run := range runs {
		job, err := s.store.ActiveJob(ctx, run.ID)
		if errors.Is(err, postgres.ErrNotFound) {
			continue
		}
		if err != nil {
			s.logger.Error("cannot read the active job of a run",
				slog.String("runId", string(run.ID)), slog.String("error", err.Error()))
			continue
		}
		if job.Status == domain.JobCancelling {
			continue
		}
		effective, err := s.store.EffectivePolicy(ctx, job.ID)
		if err != nil {
			s.logger.Error("cannot read the policy of a running job",
				slog.String("jobId", string(job.ID)), slog.String("error", err.Error()))
			continue
		}
		scope := domain.Scope{ProjectID: session.ProjectID, SessionID: session.ID, RunID: run.ID, JobID: job.ID}
		s.sendToBackend(ctx, scope, &backendv1.CoreToBackend{
			Message: &backendv1.CoreToBackend_UpdateJobPolicy{
				UpdateJobPolicy: &backendv1.UpdateJobPolicy{
					RunId:           string(run.ID),
					JobId:           string(job.ID),
					ExecutionPolicy: policyToProto(effective),
				},
			},
		})
	}
}

// SessionExecutionPolicy returns the policy a Session applies by default: its
// own if it set one, its Project's otherwise, and the refusals of both.
func (s *Service) SessionExecutionPolicy(ctx context.Context, identity auth.Identity, sessionID domain.SessionID) (domain.ExecutionPolicy, error) {
	if _, err := s.store.GetSession(ctx, identity.UserID, sessionID); err != nil {
		return domain.ExecutionPolicy{}, translate(err)
	}
	stored, err := s.store.SessionPolicy(ctx, sessionID)
	if err != nil {
		return domain.ExecutionPolicy{}, translate(err)
	}
	project, err := s.store.ProjectPolicyOfSession(ctx, sessionID)
	if err != nil {
		return domain.ExecutionPolicy{}, translate(err)
	}
	return domain.Effective(project, stored, nil), nil
}

// SessionPolicyView is what applies to a Session and where it comes from.
//
// A panel needs all three. Showing only what is in force means showing an
// inherited policy as if someone had chosen it here, which is how a change
// meant for a whole Project ends up being made one Session at a time.
type SessionPolicyView struct {
	// Effective is what actually applies, Project refusals included.
	Effective domain.ExecutionPolicy `json:"effective"`
	// Inherited reports that the Session set nothing of its own and follows its
	// Project, live: changing the Project changes this Session.
	Inherited bool `json:"inherited"`
	// Project is what it falls back to, so the panel can offer to return to it.
	Project domain.ExecutionPolicy `json:"project"`
	// Own is what this Session set, and nothing it inherited. A panel that
	// edits the effective policy edits the Project's rules into the Session,
	// quietly turning an inherited rule into a copy that stops following it.
	Own *domain.ExecutionPolicy `json:"own,omitempty"`
}

// SessionPolicyOrigin returns what applies to a Session and where it came from.
func (s *Service) SessionPolicyOrigin(ctx context.Context, identity auth.Identity, sessionID domain.SessionID) (SessionPolicyView, error) {
	if _, err := s.store.GetSession(ctx, identity.UserID, sessionID); err != nil {
		return SessionPolicyView{}, translate(err)
	}
	stored, err := s.store.SessionPolicy(ctx, sessionID)
	if err != nil {
		return SessionPolicyView{}, translate(err)
	}
	project, err := s.store.ProjectPolicyOfSession(ctx, sessionID)
	if err != nil {
		return SessionPolicyView{}, translate(err)
	}
	return SessionPolicyView{
		Effective: domain.Effective(project, stored, nil),
		Inherited: stored == nil,
		Project:   domain.Effective(project, nil, nil),
		Own:       stored,
	}, nil
}

// contradicted returns the refusal a narrower level tries to undo, if any.
func contradicted(outer *domain.ExecutionPolicy, rules []domain.PermissionRule) *domain.PermissionRule {
	if outer == nil {
		return nil
	}
	binding := outer.Binding()
	for _, rule := range rules {
		if rule.Effect == domain.PermissionDeny {
			continue
		}
		for _, bound := range binding {
			if bound.Covers(rule) {
				return &bound
			}
		}
	}
	return nil
}

// audit appends an audit entry, filling in the actor from the caller.
//
// A failure here is logged rather than returned: the act it records already
// happened, and refusing to acknowledge it would be a worse lie than a missing
// line in a log nobody is yet reading.
func (s *Service) audit(ctx context.Context, identity auth.Identity, entry postgres.AuditEntry) {
	entry.OwnerID = identity.UserID
	if entry.Channel == "" {
		entry.Channel = domain.ChannelFrom(ctx).String()
	}
	if entry.ActorID == "" {
		entry.ActorID = string(identity.UserID)
	}
	if err := s.store.RecordAudit(ctx, entry); err != nil {
		s.logger.Error("cannot record an audit entry", "action", entry.Action, "error", err)
	}
}

// Audit returns the audit trail of the caller.
func (s *Service) Audit(ctx context.Context, identity auth.Identity, limit int) ([]postgres.AuditEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	entries, err := s.store.ListAudit(ctx, identity.UserID, limit)
	return entries, translate(err)
}
