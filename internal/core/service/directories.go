package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/events"
)

func (s *Service) UpdateKnownDirectory(ctx context.Context, identity auth.Identity, id domain.KnownDirectoryID, name, description, remote *string) (domain.KnownDirectory, error) {
	for _, value := range []*string{name, description, remote} {
		if value != nil {
			*value = strings.TrimSpace(*value)
		}
	}
	if name != nil && *name == "" {
		return domain.KnownDirectory{}, fmt.Errorf("%w: a directory name is required", ErrInvalid)
	}
	dir, err := s.store.UpdateKnownDirectory(ctx, identity.UserID, id, name, description, remote)
	if err == nil {
		s.directoryChanged(ctx, identity, id)
	}
	return dir, translate(err)
}

func (s *Service) MakeKnownDirectoryGlobal(ctx context.Context, identity auth.Identity, id domain.KnownDirectoryID) (domain.KnownDirectory, error) {
	dir, err := s.store.MakeKnownDirectoryGlobal(ctx, identity.UserID, id)
	if err == nil {
		s.directoryChanged(ctx, identity, id)
	}
	return dir, translate(err)
}

func (s *Service) DeleteKnownDirectory(ctx context.Context, identity auth.Identity, id domain.KnownDirectoryID) error {
	err := s.store.DeleteKnownDirectory(ctx, identity.UserID, id)
	if err == nil {
		s.directoryChanged(ctx, identity, id)
	}
	return translate(err)
}

func (s *Service) UnbindKnownDirectory(ctx context.Context, identity auth.Identity, id domain.KnownDirectoryID, backendID domain.BackendInstanceID) error {
	if _, err := s.store.GetKnownDirectory(ctx, identity.UserID, id); err != nil {
		return translate(err)
	}
	if _, err := s.store.GetBackendInstance(ctx, identity.UserID, backendID); err != nil {
		return translate(err)
	}
	err := s.store.UnbindKnownDirectory(ctx, id, backendID)
	if err == nil {
		s.directoryChanged(ctx, identity, id)
	}
	return translate(err)
}

func (s *Service) directoryChanged(ctx context.Context, identity auth.Identity, id domain.KnownDirectoryID) {
	s.emit(ctx, identity.UserID, events.TypeDirectoryChanged, domain.Scope{}, map[string]any{"knownDirectoryId": id})
}
