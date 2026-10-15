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
