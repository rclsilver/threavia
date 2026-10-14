package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/storage/postgres"
	"github.com/rclsilver/threavia/internal/core/tools"
)

func (s *Service) scopedDirectory(ctx context.Context, identity auth.Identity, jc jobScope, raw string) (domain.KnownDirectory, error) {
	if raw == "" {
		return domain.KnownDirectory{}, fmt.Errorf("%w: a directory identifier is required", ErrInvalid)
	}
	dir, err := s.store.GetKnownDirectory(ctx, identity.UserID, domain.KnownDirectoryID(raw))
	if err != nil {
		return domain.KnownDirectory{}, translate(err)
	}
	if dir.ProjectID != "" && dir.ProjectID != jc.ProjectID {
		return domain.KnownDirectory{}, fmt.Errorf("%w: directory belongs to another project", ErrInvalid)
	}
	return dir, nil
}

func directoryInput(input map[string]any, key string) (*string, error) {
	raw, exists := input[key]
	if !exists {
		return nil, nil
	}
	value, ok := raw.(string)
	if !ok {
		return nil, fmt.Errorf("%w: %s must be a string", ErrInvalid, key)
	}
	value = strings.TrimSpace(value)
	return &value, nil
}

func (s *Service) invokeDirectoryTool(ctx context.Context, identity auth.Identity, jc jobScope, name tools.Name, input map[string]any) (map[string]any, error) {
	if name == tools.NameKnownDirectoryList {
		projectID, err := directoryProject(jc, input)
		if err != nil {
			return nil, err
		}
		dirs, err := s.ListKnownDirectories(ctx, identity, projectID)
		if err != nil {
			return nil, err
		}
		items := make([]any, 0, len(dirs))
		for _, dir := range dirs {
			bindings, err := s.ListBindings(ctx, identity, dir.ID)
			if err != nil {
				return nil, err
			}
			paths := make([]any, 0, len(bindings))
			for _, b := range bindings {
				paths = append(paths, map[string]any{"backendInstanceId": string(b.BackendInstanceID), "path": b.Path})
			}
			remote := ""
			if dir.GitRemote != nil {
				remote = *dir.GitRemote
			}
			items = append(items, map[string]any{"knownDirectoryId": string(dir.ID), "projectId": string(dir.ProjectID), "scope": directoryScope(dir), "name": dir.Name, "description": dir.Description, "gitRemote": remote, "bindings": paths})
		}
		return map[string]any{"directories": items}, nil
	}
	dir, err := s.scopedDirectory(ctx, identity, jc, text(input, "knownDirectoryId"))
	if err != nil {
		return nil, err
	}
	switch name {
	case tools.NameKnownDirectoryUpdate:
		name, err := directoryInput(input, "name")
		if err != nil {
			return nil, err
		}
		if name != nil && *name == "" {
			return nil, fmt.Errorf("%w: a directory name is required", ErrInvalid)
		}
		description, err := directoryInput(input, "description")
		if err != nil {
			return nil, err
		}
		remote, err := directoryInput(input, "gitRemote")
		if err != nil {
			return nil, err
		}
		updated, err := s.UpdateKnownDirectory(ctx, identity, dir.ID, name, description, remote)
		if err != nil {
			return nil, translate(err)
		}
		return map[string]any{"knownDirectoryId": string(updated.ID), "name": updated.Name}, nil
	case tools.NameKnownDirectoryUnbind:
		if err := s.UnbindKnownDirectory(ctx, identity, dir.ID, jc.BackendInstanceID); err != nil {
			return nil, translate(err)
		}
		return map[string]any{"knownDirectoryId": string(dir.ID), "unbound": true}, nil
	case tools.NameKnownDirectoryDelete:
		if err := s.DeleteKnownDirectory(ctx, identity, dir.ID); err != nil {
			return nil, translate(err)
		}
		return map[string]any{"knownDirectoryId": string(dir.ID), "deleted": true}, nil
	}
	return nil, fmt.Errorf("%w: unknown directory tool", ErrInvalid)
}

// Registration and its first binding succeed together, or neither is stored.
func (s *Service) registerDirectoryTool(ctx context.Context, identity auth.Identity, jc jobScope, input map[string]any) (map[string]any, error) {
	projectID, err := directoryProject(jc, input)
	if err != nil {
		return nil, err
	}
	name, path := text(input, "name"), text(input, "path")
	if name == "" || !strings.HasPrefix(path, "/") {
		return nil, fmt.Errorf("%w: a name and absolute path are required", ErrInvalid)
	}
	if projectID != "" {
		if _, err := s.store.GetProject(ctx, identity.UserID, projectID); err != nil {
			return nil, translate(err)
		}
	}
	if _, err := s.store.GetBackendInstance(ctx, identity.UserID, jc.BackendInstanceID); err != nil {
		return nil, translate(err)
	}
	dir := domain.KnownDirectory{ID: domain.NewKnownDirectoryID(), ProjectID: projectID, OwnerID: identity.UserID, Name: name, Description: text(input, "description"), GitRemote: optionalString(text(input, "gitRemote"))}
	err = s.store.WithTx(ctx, func(tx *postgres.Store) error {
		if err := tx.CreateKnownDirectory(ctx, &dir); err != nil {
			return err
		}
		return tx.BindKnownDirectory(ctx, &domain.KnownDirectoryBinding{KnownDirectoryID: dir.ID, BackendInstanceID: jc.BackendInstanceID, Path: path})
	})
	if err != nil {
		return nil, translate(err)
	}
	s.directoryChanged(ctx, identity, dir.ID)
	return map[string]any{"knownDirectoryId": string(dir.ID), "scope": directoryScope(dir), "projectId": string(dir.ProjectID)}, nil
}

func directoryProject(jc jobScope, input map[string]any) (domain.ProjectID, error) {
	value, err := directoryInput(input, "scope")
	if err != nil {
		return "", err
	}
	if value == nil || *value == "project" {
		return jc.ProjectID, nil
	}
	if *value == "global" {
		return "", nil
	}
	return "", fmt.Errorf("%w: scope must be project or global", ErrInvalid)
}

func directoryScope(dir domain.KnownDirectory) string {
	if dir.ProjectID == "" {
		return "global"
	}
	return "project"
}
