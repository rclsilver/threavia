package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/events"
	"github.com/rclsilver/threavia/internal/core/storage/s3"
)

// CreateProject creates a Project owned by the caller.
func (s *Service) CreateProject(ctx context.Context, identity auth.Identity, name, description string) (domain.Project, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return domain.Project{}, fmt.Errorf("%w: a project name is required", ErrInvalid)
	}

	project := domain.Project{
		ID:          domain.NewProjectID(),
		OwnerID:     identity.UserID,
		Name:        name,
		Description: strings.TrimSpace(description),
		Status:      domain.ProjectActive,
	}
	if err := s.store.CreateProject(ctx, &project); err != nil {
		return domain.Project{}, translate(err)
	}
	return project, nil
}

// ListProjects returns the Projects of the caller.
func (s *Service) ListProjects(ctx context.Context, identity auth.Identity, includeArchived bool) ([]domain.Project, error) {
	projects, err := s.store.ListProjects(ctx, identity.UserID, includeArchived)
	return projects, translate(err)
}

// GetProject returns one Project of the caller.
func (s *Service) GetProject(ctx context.Context, identity auth.Identity, id domain.ProjectID) (domain.Project, error) {
	project, err := s.store.GetProject(ctx, identity.UserID, id)
	return project, translate(err)
}

// SetProjectStatus archives or restores a Project. Archiving is the normal
// lifecycle; permanent deletion is a separate, explicit operation.
func (s *Service) SetProjectStatus(ctx context.Context, identity auth.Identity, id domain.ProjectID, status domain.ProjectStatus) (domain.Project, error) {
	current, err := s.store.GetProject(ctx, identity.UserID, id)
	if err != nil {
		return domain.Project{}, translate(err)
	}
	if _, err := current.Status.Transition(status); err != nil {
		return domain.Project{}, fmt.Errorf("%w: %s", ErrConflict, err)
	}
	project, err := s.store.SetProjectStatus(ctx, identity.UserID, id, status)
	return project, translate(err)
}

// DeleteProject permanently removes a Project and everything it owns.
// BackendInstances belong to the user and are never deleted with it.
func (s *Service) DeleteProject(ctx context.Context, identity auth.Identity, id domain.ProjectID) error {
	keys, err := s.store.DeleteProjectData(ctx, identity.UserID, id)
	if err != nil {
		return translate(err)
	}
	for _, key := range keys {
		if s.objects != nil {
			if err := s.objects.Delete(ctx, key); err != nil && !errors.Is(err, s3.ErrObjectNotFound) {
				s.logger.Error("cannot remove a deleted project's artifact bytes", "objectKey", key, "error", err)
			}
		}
	}
	s.emit(ctx, identity.UserID, events.TypeProjectDeleted, domain.Scope{}, map[string]any{"projectId": id})
	return nil
}

// CreateKnownDirectory registers a project-level logical directory.
func (s *Service) CreateKnownDirectory(ctx context.Context, identity auth.Identity, projectID domain.ProjectID, name, description string, gitRemote *string) (domain.KnownDirectory, error) {
	if _, err := s.store.GetProject(ctx, identity.UserID, projectID); err != nil {
		return domain.KnownDirectory{}, translate(err)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return domain.KnownDirectory{}, fmt.Errorf("%w: a directory name is required", ErrInvalid)
	}

	dir := domain.KnownDirectory{
		ID:          domain.NewKnownDirectoryID(),
		ProjectID:   projectID,
		Name:        name,
		Description: strings.TrimSpace(description),
		GitRemote:   gitRemote,
	}
	if err := s.store.CreateKnownDirectory(ctx, &dir); err != nil {
		return domain.KnownDirectory{}, translate(err)
	}
	return dir, nil
}

// ListKnownDirectories returns the logical directories of a Project.
func (s *Service) ListKnownDirectories(ctx context.Context, identity auth.Identity, projectID domain.ProjectID) ([]domain.KnownDirectory, error) {
	dirs, err := s.store.ListKnownDirectories(ctx, identity.UserID, projectID)
	return dirs, translate(err)
}

// BindKnownDirectory records where a logical directory lives on one backend.
//
// The path is backend truth and is never validated here: Core has no access to
// the backend filesystem, and a KnownDirectory is portability metadata, not a
// sandbox.
func (s *Service) BindKnownDirectory(ctx context.Context, identity auth.Identity, dirID domain.KnownDirectoryID, instanceID domain.BackendInstanceID, path string) (domain.KnownDirectoryBinding, error) {
	if _, err := s.store.GetKnownDirectory(ctx, identity.UserID, dirID); err != nil {
		return domain.KnownDirectoryBinding{}, translate(err)
	}
	if _, err := s.store.GetBackendInstance(ctx, identity.UserID, instanceID); err != nil {
		return domain.KnownDirectoryBinding{}, translate(err)
	}
	path = strings.TrimSpace(path)
	if !strings.HasPrefix(path, "/") {
		return domain.KnownDirectoryBinding{}, fmt.Errorf("%w: the binding path must be absolute", ErrInvalid)
	}

	binding := domain.KnownDirectoryBinding{
		KnownDirectoryID:  dirID,
		BackendInstanceID: instanceID,
		Path:              path,
	}
	if err := s.store.BindKnownDirectory(ctx, &binding); err != nil {
		return domain.KnownDirectoryBinding{}, translate(err)
	}
	return binding, nil
}

// ListBindings returns every backend binding of a logical directory.
func (s *Service) ListBindings(ctx context.Context, identity auth.Identity, dirID domain.KnownDirectoryID) ([]domain.KnownDirectoryBinding, error) {
	bindings, err := s.store.ListBindings(ctx, identity.UserID, dirID)
	return bindings, translate(err)
}

// UpdateProject changes the name, description and instructions of a Project.
//
// Instructions are the provider-independent project rules of specification
// section 18: what every agent working on this Project must follow, whatever
// provider runs it.
func (s *Service) UpdateProject(ctx context.Context, identity auth.Identity, id domain.ProjectID, name, description, instructions string) (domain.Project, error) {
	current, err := s.store.GetProject(ctx, identity.UserID, id)
	if err != nil {
		return domain.Project{}, translate(err)
	}

	// Absent fields keep their value: a client editing the instructions must not
	// have to resend the name.
	if name = strings.TrimSpace(name); name == "" {
		name = current.Name
	}
	if description == "" {
		description = current.Description
	}
	if instructions == "" {
		instructions = current.Instructions
	}

	project, err := s.store.UpdateProject(ctx, identity.UserID, id,
		name, strings.TrimSpace(description), strings.TrimSpace(instructions))
	if err != nil {
		return project, translate(err)
	}
	s.emit(ctx, identity.UserID, events.TypeProjectUpdated, domain.Scope{ProjectID: id}, map[string]any{"projectId": id, "name": project.Name})
	return project, nil
}
