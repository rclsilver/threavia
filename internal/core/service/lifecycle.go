package service

import (
	"context"
	"encoding/json"

	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/events"
	"github.com/rclsilver/threavia/internal/core/storage/postgres"
)

// This audit is part of the mutation's transaction, rather than best effort.
// Subject identifiers remain readable after their rows have been deleted.
func (s *Service) recordLifecycleAudit(ctx context.Context, tx *postgres.Store, identity auth.Identity, kind events.Type, scope domain.Scope, subject string, payload any) error {
	detail, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	actor, jobID := string(identity.UserID), managerJobFrom(ctx)
	if jobID != "" {
		actor = "agent"
	}
	return tx.RecordAudit(ctx, postgres.AuditEntry{
		OwnerID: identity.UserID, ActorID: actor, Action: string(kind),
		ProjectID: string(scope.ProjectID), SessionID: string(scope.SessionID),
		JobID: string(jobID), SubjectID: subject, Channel: domain.ChannelFrom(ctx).String(), Detail: detail,
	})
}

// recordAuditedChange couples the durable notification to the mutation and
// audit. The caller flushes its batch only after the transaction commits.
func (s *Service) recordAuditedChange(ctx context.Context, tx *postgres.Store, b *batch, identity auth.Identity, kind events.Type, scope domain.Scope, subject string, payload any) error {
	if err := s.recordLifecycleAudit(ctx, tx, identity, kind, scope, subject, payload); err != nil {
		return err
	}
	return s.appendAll(ctx, tx, b, record{eventType: kind, scope: scope, payload: payload})
}
