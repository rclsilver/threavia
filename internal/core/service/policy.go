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

// SetSessionExecutionPolicy sets what the agent may do in a Session by default.
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
	if err := s.store.SetSessionExecutionPolicy(ctx, identity.UserID, sessionID, policy); err != nil {
		return domain.ExecutionPolicy{}, translate(err)
	}

	effective := domain.Effective(policy, nil)
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

// SessionExecutionPolicy returns the policy a Session applies by default.
func (s *Service) SessionExecutionPolicy(ctx context.Context, identity auth.Identity, sessionID domain.SessionID) (domain.ExecutionPolicy, error) {
	if _, err := s.store.GetSession(ctx, identity.UserID, sessionID); err != nil {
		return domain.ExecutionPolicy{}, translate(err)
	}
	stored, err := s.store.SessionPolicy(ctx, sessionID)
	if err != nil {
		return domain.ExecutionPolicy{}, translate(err)
	}
	return domain.Effective(stored, nil), nil
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
