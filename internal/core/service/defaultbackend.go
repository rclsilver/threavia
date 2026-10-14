package service

import (
	"context"

	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/events"
)

func (s *Service) SetDefaultBackend(ctx context.Context, identity auth.Identity, id domain.BackendInstanceID) error {
	if err := s.store.SetDefaultBackend(ctx, identity.UserID, id); err != nil {
		return translate(err)
	}
	s.emit(ctx, identity.UserID, events.TypeBackendDefaultChanged, domain.Scope{}, map[string]any{"backendInstanceId": id})
	return nil
}
