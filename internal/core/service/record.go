package service

import (
	"context"

	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/events"
	"github.com/rclsilver/threavia/internal/core/storage/postgres"
)

// record describes one event to append, so a transaction can declare its whole
// timeline contribution in one place.
type record struct {
	eventType events.Type
	scope     domain.Scope
	payload   any
}

// appendAll persists several events inside a transaction and stages them for
// publication. Nothing reaches a client until the transaction commits.
func (s *Service) appendAll(ctx context.Context, tx *postgres.Store, b *batch, records ...record) error {
	for _, r := range records {
		built, err := s.newRecord(r.eventType, r.scope, r.payload)
		if err != nil {
			return err
		}
		built.OwnerID = &b.ownerID
		if err := tx.AppendEvent(ctx, built); err != nil {
			return err
		}
		b.add(built)
	}
	return nil
}

// emit appends a single event outside a transaction and publishes it.
//
// A failure here is logged rather than returned: the state change it describes
// is already committed, and losing the notification only costs a client a
// refresh, while failing the whole operation would be a lie.
func (s *Service) emit(ctx context.Context, ownerID domain.UserID, eventType events.Type, scope domain.Scope, payload any) {
	built, err := s.newRecord(eventType, scope, payload)
	if err != nil {
		s.logger.Error("cannot build event", "type", eventType, "error", err)
		return
	}
	built.OwnerID = &ownerID
	if err := s.store.AppendEvent(ctx, built); err != nil {
		s.logger.Error("cannot persist event", "type", eventType, "error", err)
		return
	}
	s.broker.Publish(ownerID, built.Envelope)
}
