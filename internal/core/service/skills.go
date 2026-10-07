package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/skills"
)

// skillMimeType is what a packed Skill bundle is stored as.
const skillMimeType = "application/gzip"

// SetSkillAcquirer installs the component that fetches Skills. Without one, and
// without object storage, Skill installation refuses rather than pretending.
func (s *Service) SetSkillAcquirer(acquirer *skills.Acquirer) { s.skills = acquirer }

// InstallSkill acquires a Skill, stores its immutable bundle and records its
// provenance (spec section 18).
//
// Core fetches, inspects and repacks. It never runs anything the source
// contains: the only thing that ever executes a Skill is an agent, on a backend,
// under that backend's own permissions.
func (s *Service) InstallSkill(ctx context.Context, identity auth.Identity, projectID domain.ProjectID, source domain.SkillSource, name string, upload io.Reader) (domain.Skill, error) {
	if s.skills == nil || s.objects == nil {
		return domain.Skill{}, ErrStorageUnavailable
	}
	if !source.Type.Valid() {
		return domain.Skill{}, fmt.Errorf("%w: unknown skill source type %q", ErrInvalid, source.Type)
	}
	if source.Type != domain.SkillSourceUpload && source.URL == "" {
		return domain.Skill{}, fmt.Errorf("%w: a %s skill needs a url", ErrInvalid, source.Type)
	}
	project, err := s.store.GetProject(ctx, identity.UserID, projectID)
	if err != nil {
		return domain.Skill{}, translate(err)
	}

	bundle, err := s.skills.Acquire(ctx, source, upload)
	if err != nil {
		if errors.Is(err, skills.ErrInvalidSkill) {
			return domain.Skill{}, fmt.Errorf("%w: %s", ErrInvalid, err)
		}
		return domain.Skill{}, err
	}
	if name == "" {
		name = bundle.Name
	}
	if name == "" {
		return domain.Skill{}, fmt.Errorf("%w: the skill has no name", ErrInvalid)
	}

	// The bundle is stored as an Artifact: immutable bytes with a checksum is
	// exactly what section 19 already provides.
	artifact, err := s.UploadArtifact(ctx, identity, project.ID,
		name+"-"+bundle.InstalledRevision+".tar.gz", skillMimeType,
		bytes.NewReader(bundle.Content), domain.Scope{})
	if err != nil {
		return domain.Skill{}, err
	}

	skill := domain.Skill{
		ID:                domain.NewSkillID(),
		OwnerID:           identity.UserID,
		ProjectID:         project.ID,
		Name:              name,
		Description:       bundle.Description,
		Source:            source,
		InstalledRevision: bundle.InstalledRevision,
		ArtifactID:        artifact.ID,
		BundleSHA256:      bundle.SHA256,
	}
	replaced, err := s.store.UpsertSkill(ctx, &skill)
	if err != nil {
		// The bundle is already stored; an orphaned blob is worse than a failed
		// request, and the Artifact is the user's to keep or drop otherwise.
		if removeErr := s.DeleteArtifact(ctx, identity, artifact.ID); removeErr != nil {
			s.logger.Error("cannot remove the bundle of a failed install",
				"artifactId", artifact.ID, "error", removeErr)
		}
		return domain.Skill{}, translate(err)
	}
	// A new version supersedes the old bundle: nothing points at it any more, and
	// keeping every version a Project ever installed is not what was asked for.
	if replaced != nil {
		if err := s.DeleteArtifact(ctx, identity, *replaced); err != nil && !errors.Is(err, ErrNotFound) {
			s.logger.Error("cannot remove the superseded bundle of a skill",
				"artifactId", *replaced, "error", err)
		}
	}
	return skill, nil
}

// ListSkills returns the Core-managed Skills of a Project.
func (s *Service) ListSkills(ctx context.Context, identity auth.Identity, projectID domain.ProjectID) ([]domain.Skill, error) {
	if _, err := s.store.GetProject(ctx, identity.UserID, projectID); err != nil {
		return nil, translate(err)
	}
	installed, err := s.store.ListSkills(ctx, projectID)
	return installed, translate(err)
}

// UninstallSkill removes a Skill. The bundle it referenced goes with it: nothing
// else can point at it, because a Skill owns its own Artifact.
func (s *Service) UninstallSkill(ctx context.Context, identity auth.Identity, id domain.SkillID) error {
	artifactID, err := s.store.DeleteSkill(ctx, identity.UserID, id)
	if err != nil {
		return translate(err)
	}
	if err := s.DeleteArtifact(ctx, identity, artifactID); err != nil && !errors.Is(err, ErrNotFound) {
		s.logger.Error("cannot remove the bundle of an uninstalled skill",
			"artifactId", artifactID, "error", err)
	}
	return nil
}

// OpenSkillBundle opens the packed bundle of a Skill, for distribution to a
// backend. It takes no user identity: the caller is a BackendInstance that was
// already told, by a Job, which Skills apply.
func (s *Service) OpenSkillBundle(ctx context.Context, id domain.SkillID) (domain.Skill, io.ReadCloser, error) {
	skill, err := s.store.SkillByID(ctx, id)
	if err != nil {
		return domain.Skill{}, nil, translate(err)
	}
	if s.objects == nil {
		return skill, nil, ErrStorageUnavailable
	}

	artifact, err := s.store.GetArtifact(ctx, skill.OwnerID, skill.ArtifactID)
	if err != nil {
		return skill, nil, translate(err)
	}
	body, _, err := s.objects.Get(ctx, artifact.ObjectKey)
	if err != nil {
		return skill, nil, fmt.Errorf("%w: the skill bundle is missing", ErrNotFound)
	}
	return skill, body, nil
}

// RecordBackendSkills stores what a BackendInstance reports it has locally.
// Core keeps the metadata only; the content never leaves the backend.
func (s *Service) RecordBackendSkills(ctx context.Context, backendID domain.BackendInstanceID, local []domain.BackendSkill) error {
	return translate(s.store.ReplaceBackendSkills(ctx, backendID, local))
}

// BackendSkills returns the local Skills a BackendInstance reported, which is
// what lets a handoff say that a Skill is unavailable elsewhere.
func (s *Service) BackendSkills(ctx context.Context, identity auth.Identity, backendID domain.BackendInstanceID) ([]domain.BackendSkill, error) {
	if _, err := s.store.GetBackendInstance(ctx, identity.UserID, backendID); err != nil {
		return nil, translate(err)
	}
	local, err := s.store.ListBackendSkills(ctx, backendID)
	return local, translate(err)
}

// SkillBundle implements the backendconn Sink: it serves a bundle to a
// BackendInstance that asked for one.
//
// The instance must belong to the same owner as the Skill. A backend is a
// machine someone registered, and nothing says the Skills of one user's Project
// may travel to another user's laptop.
func (s *Service) SkillBundle(ctx context.Context, instanceID domain.BackendInstanceID, skillID string) (io.ReadCloser, error) {
	skill, body, err := s.OpenSkillBundle(ctx, domain.SkillID(skillID))
	if err != nil {
		return nil, err
	}

	instance, err := s.store.BackendInstanceByID(ctx, instanceID)
	if err != nil {
		_ = body.Close()
		return nil, translate(err)
	}
	if instance.OwnerID == nil || *instance.OwnerID != skill.OwnerID {
		_ = body.Close()
		return nil, ErrNotFound
	}
	return body, nil
}

// SkillInventory implements the backendconn Sink.
func (s *Service) SkillInventory(ctx context.Context, instanceID domain.BackendInstanceID, inventory *backendv1.SkillInventory) {
	local := make([]domain.BackendSkill, 0, len(inventory.GetLocal()))
	for _, skill := range inventory.GetLocal() {
		if skill.GetName() == "" {
			continue
		}
		local = append(local, domain.BackendSkill{
			Name:        skill.GetName(),
			Description: skill.GetDescription(),
			Available:   skill.GetAvailable(),
		})
	}

	if err := s.RecordBackendSkills(ctx, instanceID, local); err != nil {
		s.logger.Error("cannot record the backend skills",
			"backendInstanceId", instanceID, "error", err)
	}
}
