package service

import (
	"context"
	"slices"

	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/events"
	"github.com/rclsilver/threavia/internal/core/storage/postgres"
)

// changeBackendState commits the lifecycle change and its notification together.
// Locking the instance also keeps an old connection's disconnect from parking
// the work of its replacement. Clients only hear about committed state.
func (s *Service) changeBackendState(ctx context.Context, id domain.BackendInstanceID, change func(*postgres.Store, domain.BackendInstance) error) error {
	b := &batch{}
	err := s.store.WithTx(ctx, func(tx *postgres.Store) error {
		before, err := tx.LockBackendInstance(ctx, id)
		if err != nil {
			return err
		}
		beforeConditions, err := tx.BackendConditions(ctx, []domain.BackendInstanceID{id})
		if err != nil {
			return err
		}
		if err := change(tx, before); err != nil {
			return err
		}
		after, err := tx.BackendInstanceByID(ctx, id)
		if err != nil {
			return err
		}
		afterConditions, err := tx.BackendConditions(ctx, []domain.BackendInstanceID{id})
		if err != nil {
			return err
		}
		if after.OwnerID == nil || sameBackendState(before, after, beforeConditions[id], afterConditions[id]) {
			return nil
		}
		b.ownerID = *after.OwnerID
		// Keep STARTING, lease replacement and repeated reports out of the
		// audit. Record loss of availability and arrival at READY.
		if before.OperationalStatus != after.OperationalStatus &&
			(after.OperationalStatus == domain.BackendDegraded || after.OperationalStatus == domain.BackendOffline ||
				after.OperationalStatus == domain.BackendReady) {
			actor, channel := "backend:"+string(id), "backend"
			if after.OperationalStatus == domain.BackendOffline {
				actor, channel = "core", "core"
			}
			if err := recordBackendAudit(ctx, tx, after, actor, channel, "backend.availability_changed", map[string]any{
				"from": before.OperationalStatus, "to": after.OperationalStatus, "providerAuthState": after.ProviderAuthState,
			}); err != nil {
				return err
			}
		}
		return s.appendAll(ctx, tx, b, record{
			eventType: events.TypeBackendStatusChanged,
			payload: map[string]any{
				"backendInstanceId": id,
				"operationalStatus": after.OperationalStatus,
				"providerAuthState": after.ProviderAuthState,
			},
		})
	})
	if err == nil {
		s.flush(b)
	}
	return err
}

func sameBackendState(before, after domain.BackendInstance, oldConditions, newConditions []domain.Condition) bool {
	lease := func(id *string) string {
		if id == nil {
			return ""
		}
		return *id
	}
	return before.OperationalStatus == after.OperationalStatus &&
		before.ProviderAuthState == after.ProviderAuthState &&
		before.Capacity == after.Capacity &&
		lease(before.ConnectionID) == lease(after.ConnectionID) &&
		slices.EqualFunc(oldConditions, newConditions, func(a, b domain.Condition) bool {
			// A fresh observation of the same report is not a state change.
			return a.Type == b.Type && a.Status == b.Status && a.Reason == b.Reason && a.Message == b.Message
		})
}
