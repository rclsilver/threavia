package service

import (
	"context"
	"fmt"

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
	return effective, nil
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
