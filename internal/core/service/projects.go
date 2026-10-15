package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/events"
	"github.com/rclsilver/threavia/internal/core/storage/postgres"
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
	b := &batch{ownerID: identity.UserID}
	if err := s.store.WithTx(ctx, func(tx *postgres.Store) error {
		if err := tx.CreateProject(ctx, &project); err != nil {
			return err
		}
		return s.recordAuditedChange(ctx, tx, b, identity, events.TypeProjectCreated, domain.Scope{ProjectID: project.ID}, string(project.ID), projectAuditPayload(project))
	}); err != nil {
		return domain.Project{}, translate(err)
	}
	s.flush(b)
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
	var project domain.Project
	b := &batch{ownerID: identity.UserID}
	err := s.store.WithTx(ctx, func(tx *postgres.Store) error {
		current, err := tx.LockProject(ctx, identity.UserID, id)
		if err != nil {
			return err
		}
		if _, err := current.Status.Transition(status); err != nil {
			return fmt.Errorf("%w: %s", ErrConflict, err)
		}
		project, err = tx.SetProjectStatus(ctx, identity.UserID, id, status)
		if err != nil {
			return err
		}
		kind := events.TypeProjectArchived
		if status == domain.ProjectActive {
			kind = events.TypeProjectRestored
		}
		return s.recordAuditedChange(ctx, tx, b, identity, kind, domain.Scope{ProjectID: id}, string(id), projectAuditPayload(project))
	})
	if err != nil {
		return domain.Project{}, translate(err)
	}
	s.flush(b)
	return project, nil
}

// DeleteProject permanently removes a Project and everything it owns.
// BackendInstances belong to the user and are never deleted with it.
func (s *Service) DeleteProject(ctx context.Context, identity auth.Identity, id domain.ProjectID) error {
	var keys []string
	b := &batch{ownerID: identity.UserID}
	err := s.store.WithTx(ctx, func(tx *postgres.Store) error {
		if err := tx.LockDecisionMemory(ctx, identity.UserID); err != nil {
			return err
		}
		project, err := tx.LockProject(ctx, identity.UserID, id)
		if err != nil {
			return err
		}
		keys, err = tx.DeleteProjectData(ctx, identity.UserID, id)
		if err != nil {
			return err
		}
		payload := projectAuditPayload(project)
		if err := s.recordLifecycleAudit(ctx, tx, identity, events.TypeProjectDeleted, domain.Scope{ProjectID: id}, string(id), payload); err != nil {
			return err
		}
		// The owner-scoped notice survives the project's cascading deletion.
		return s.appendAll(ctx, tx, b, record{eventType: events.TypeProjectDeleted, scope: domain.Scope{}, payload: payload})
	})
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
	s.flush(b)
	return nil
}

// CreateKnownDirectory registers a project directory, or a global one with an empty projectID.
func (s *Service) CreateKnownDirectory(ctx context.Context, identity auth.Identity, projectID domain.ProjectID, name, description string, gitRemote *string) (domain.KnownDirectory, error) {
	if projectID != "" {
		if _, err := s.store.GetProject(ctx, identity.UserID, projectID); err != nil {
			return domain.KnownDirectory{}, translate(err)
		}
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return domain.KnownDirectory{}, fmt.Errorf("%w: a directory name is required", ErrInvalid)
	}

	dir := domain.KnownDirectory{
		ID:          domain.NewKnownDirectoryID(),
		ProjectID:   projectID,
		OwnerID:     identity.UserID,
		Name:        name,
		Description: strings.TrimSpace(description),
		GitRemote:   gitRemote,
	}
	if err := s.store.CreateKnownDirectory(ctx, &dir); err != nil {
		return domain.KnownDirectory{}, translate(err)
	}
	s.directoryChanged(ctx, identity, dir.ID)
	return dir, nil
}

// ListKnownDirectories includes the caller's globals; an empty projectID lists only globals.
func (s *Service) ListKnownDirectories(ctx context.Context, identity auth.Identity, projectID domain.ProjectID) ([]domain.KnownDirectory, error) {
	if projectID != "" {
		if _, err := s.store.GetProject(ctx, identity.UserID, projectID); err != nil {
			return nil, translate(err)
		}
	}
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
	s.directoryChanged(ctx, identity, dirID)
	return binding, nil
}

// ListBindings returns every backend binding of a logical directory.
func (s *Service) ListBindings(ctx context.Context, identity auth.Identity, dirID domain.KnownDirectoryID) ([]domain.KnownDirectoryBinding, error) {
	if _, err := s.store.GetKnownDirectory(ctx, identity.UserID, dirID); err != nil {
		return nil, translate(err)
	}
	bindings, err := s.store.ListBindings(ctx, identity.UserID, dirID)
	return bindings, translate(err)
}

// UpdateProject changes the name, description and instructions of a Project.
//
// Instructions are the provider-independent project rules of specification
// section 18: what every agent working on this Project must follow, whatever
// provider runs it.
func (s *Service) UpdateProject(ctx context.Context, identity auth.Identity, id domain.ProjectID, name, description, instructions string) (domain.Project, error) {
	var project domain.Project
	b := &batch{ownerID: identity.UserID}
	err := s.store.WithTx(ctx, func(tx *postgres.Store) error {
		current, err := tx.LockProject(ctx, identity.UserID, id)
		if err != nil {
			return err
		}
		// Absent fields retain the existing API's patch semantics.
		if name = strings.TrimSpace(name); name == "" {
			name = current.Name
		}
		if description == "" {
			description = current.Description
		}
		if instructions == "" {
			instructions = current.Instructions
		}
		description, instructions = strings.TrimSpace(description), strings.TrimSpace(instructions)
		var fields []string
		if name != current.Name {
			fields = append(fields, "name")
		}
		if description != current.Description {
			fields = append(fields, "description")
		}
		if instructions != current.Instructions {
			fields = append(fields, "instructions")
		}
		if len(fields) == 0 {
			project = current
			return nil
		}
		project, err = tx.UpdateProject(ctx, identity.UserID, id, name, description, instructions)
		if err != nil {
			return err
		}
		payload := projectAuditPayload(project)
		payload.ChangedFields = fields
		if name != current.Name {
			payload.PreviousName = current.Name
		}
		return s.recordAuditedChange(ctx, tx, b, identity, events.TypeProjectUpdated, domain.Scope{ProjectID: id}, string(id), payload)
	})
	if err != nil {
		return domain.Project{}, translate(err)
	}
	s.flush(b)
	return project, nil
}

type ProjectPayload struct {
	ProjectID     domain.ProjectID     `json:"projectId"`
	Name          string               `json:"name"`
	Status        domain.ProjectStatus `json:"status"`
	PreviousName  string               `json:"previousName,omitempty"`
	ChangedFields []string             `json:"changedFields,omitempty"`
}

func projectAuditPayload(project domain.Project) ProjectPayload {
	return ProjectPayload{ProjectID: project.ID, Name: project.Name, Status: project.Status}
}
