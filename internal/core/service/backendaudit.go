package service

import (
	"context"
	"encoding/json"

	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/storage/postgres"
)

// Backend audit is account-scoped. It never copies registration material,
// credentials, provider conditions or connection identifiers.
func recordBackendAudit(ctx context.Context, tx *postgres.Store, instance domain.BackendInstance, actor, channel, action string, detail map[string]any) error {
	if instance.OwnerID == nil {
		return nil
	}
	detail["name"] = instance.Name
	payload, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	return tx.RecordAudit(ctx, postgres.AuditEntry{
		OwnerID: *instance.OwnerID, ActorID: actor, Action: action,
		SubjectID: string(instance.ID), Channel: channel, Detail: payload,
	})
}
