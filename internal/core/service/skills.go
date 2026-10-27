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
	"github.com/rclsilver/threavia/internal/core/events"
	"github.com/rclsilver/threavia/internal/core/skills"
	"github.com/rclsilver/threavia/internal/core/storage/postgres"
	"github.com/rclsilver/threavia/internal/core/storage/s3"
)

// skillMimeType is what a packed Skill bundle is stored as.
const skillMimeType = "application/gzip"

// SetSkillAcquirer installs the component that fetches Skills. Without one, and
// without object storage, Skill installation refuses rather than pretending.
func (s *Service) SetSkillAcquirer(acquirer *skills.Acquirer) { s.skills = acquirer }

// InstallSkill acquires a Skill, stores its immutable bundle and records its
// provenance (spec section 18). Without a Project, the Skill is global.
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
	if projectID != "" {
		if _, err := s.store.GetProject(ctx, identity.UserID, projectID); err != nil {
			return domain.Skill{}, translate(err)
		}
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
	artifact, err := s.storeSkillBundle(ctx, identity, projectID,
		name+"-"+bundle.InstalledRevision+".tar.gz", bundle.Content)
	if err != nil {
		return domain.Skill{}, err
	}

	skill := domain.Skill{
		ID:                domain.NewSkillID(),
		OwnerID:           identity.UserID,
		ProjectID:         projectID,
		Name:              name,
		Description:       bundle.Description,
		Source:            source,
		InstalledRevision: bundle.InstalledRevision,
		ArtifactID:        artifact.ID,
		BundleSHA256:      bundle.SHA256,
	}
	var replaced *domain.ArtifactID
	b := &batch{ownerID: identity.UserID}
	err = s.store.WithTx(ctx, func(tx *postgres.Store) error {
		if err := lockSkills(ctx, tx, identity.UserID, projectID); err != nil {
			return err
		}
		var err error
		replaced, err = tx.UpsertSkill(ctx, &skill)
		if err != nil {
			return err
		}
		payload := map[string]any{"skillId": skill.ID, "name": skill.Name, "installedRevision": skill.InstalledRevision, "replaced": replaced != nil}
		return s.recordSkillChange(ctx, tx, b, identity, skill, events.TypeSkillInstalled, payload)
	})
	if err != nil {
		// The bundle is already stored; an orphaned blob is worse than a failed
		// request, and the Artifact is the user's to keep or drop otherwise.
		if removeErr := s.DeleteArtifact(ctx, identity, artifact.ID); removeErr != nil {
			s.logger.Error("cannot remove the bundle of a failed install",
				"artifactId", artifact.ID, "error", removeErr)
		}
		return domain.Skill{}, translate(err)
	}
	s.flush(b)
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

// storeSkillBundle stores the packed bundle of a Skill. A Project Skill's is an
// Artifact of the Project; a global Skill's belongs to its owner alone, so
// deleting a Project never removes what the others use.
func (s *Service) storeSkillBundle(ctx context.Context, identity auth.Identity, projectID domain.ProjectID, filename string, content []byte) (domain.Artifact, error) {
	if projectID != "" {
		return s.UploadArtifact(ctx, identity, projectID, filename, skillMimeType, bytes.NewReader(content), domain.Scope{})
	}
	artifact := domain.Artifact{
		ID:       domain.NewArtifactID(),
		OwnerID:  identity.UserID,
		Filename: sanitiseFilename(filename),
		MimeType: skillMimeType,
	}
	artifact.ObjectKey = s3.ObjectKey("", string(artifact.ID))
	return artifact, s.storeArtifact(ctx, &artifact, bytes.NewReader(content))
}

// lockSkills serializes the changes to the Skills of a Project, or to the
// global Skills of an owner when there is no Project.
func lockSkills(ctx context.Context, tx *postgres.Store, ownerID domain.UserID, projectID domain.ProjectID) error {
	if projectID == "" {
		return tx.LockGlobalSkills(ctx, ownerID)
	}
	return tx.LockProjectSkills(ctx, ownerID, projectID)
}

// ListGlobalSkills returns the global Skills of the user, each with the
// Projects that turned it off.
func (s *Service) ListGlobalSkills(ctx context.Context, identity auth.Identity) ([]domain.Skill, error) {
	installed, err := s.store.ListGlobalSkills(ctx, identity.UserID)
	return installed, translate(err)
}

// MakeSkillGlobal moves a Project Skill to the user's global Skills, keeping its
// identifier and bundle. Every other Project then uses it too, unless it has a
// Skill of the same name. A global Skill is returned unchanged.
func (s *Service) MakeSkillGlobal(ctx context.Context, identity auth.Identity, id domain.SkillID) (domain.Skill, error) {
	skill, err := s.store.GetSkill(ctx, identity.UserID, id)
	if err != nil {
		return domain.Skill{}, translate(err)
	}
	if skill.ProjectID == "" {
		return skill, nil
	}
	previous := skill.ProjectID
	b := &batch{ownerID: identity.UserID}
	err = s.store.WithTx(ctx, func(tx *postgres.Store) error {
		// Always the Project before the owner, as nothing else takes both.
		if err := tx.LockProjectSkills(ctx, identity.UserID, previous); err != nil {
			return err
		}
		if err := tx.LockGlobalSkills(ctx, identity.UserID); err != nil {
			return err
		}
		moved, err := tx.MakeSkillGlobal(ctx, identity.UserID, id)
		if err != nil {
			return err
		}
		skill = moved
		payload := map[string]any{"skillId": skill.ID, "name": skill.Name, "action": "made_global", "previousProjectId": previous}
		return s.recordSkillChange(ctx, tx, b, identity, skill, events.TypeSkillChanged, payload)
	})
	if errors.Is(err, postgres.ErrConflict) {
		return domain.Skill{}, fmt.Errorf("%w: a global skill is already named %q; uninstall one of them first", ErrConflict, skill.Name)
	}
	if err != nil {
		return domain.Skill{}, translate(err)
	}
	s.flush(b)
	return skill, nil
}

// SetSkillProjectEnabled turns a global Skill on or off in one Project. A
// Project Skill is not turned off, it is uninstalled.
func (s *Service) SetSkillProjectEnabled(ctx context.Context, identity auth.Identity, projectID domain.ProjectID, id domain.SkillID, enabled bool) error {
	project, err := s.store.GetProject(ctx, identity.UserID, projectID)
	if err != nil {
		return translate(err)
	}
	skill, err := s.store.GetSkill(ctx, identity.UserID, id)
	if err != nil {
		return translate(err)
	}
	if skill.ProjectID != "" {
		return fmt.Errorf("%w: only a global skill can be turned off in a project", ErrInvalid)
	}
	action := "disabled"
	if enabled {
		action = "enabled"
	}
	b := &batch{ownerID: identity.UserID}
	err = s.store.WithTx(ctx, func(tx *postgres.Store) error {
		if err := tx.SetSkillProjectEnabled(ctx, id, projectID, enabled); err != nil {
			return err
		}
		// The audit keeps naming the Project after it is renamed or deleted.
		payload := map[string]any{"skillId": skill.ID, "name": skill.Name, "action": action, "projectName": project.Name}
		return s.recordAuditedChange(ctx, tx, b, identity, events.TypeSkillChanged, domain.Scope{ProjectID: projectID}, string(skill.ID), payload)
	})
	if err != nil {
		return translate(err)
	}
	s.flush(b)
	return nil
}

// ListSkills returns the Core-managed Skills of a Project, without the global
// ones.
func (s *Service) ListSkills(ctx context.Context, identity auth.Identity, projectID domain.ProjectID) ([]domain.Skill, error) {
	if _, err := s.store.GetProject(ctx, identity.UserID, projectID); err != nil {
		return nil, translate(err)
	}
	installed, err := s.store.ListSkills(ctx, projectID)
	return installed, translate(err)
}

// skillTextLimit is the largest file of a Skill whose text is shown. A Skill
// is instructions and small scripts; anything larger is listed by size.
const skillTextLimit = 256 << 10

// SkillFiles returns the files of an installed Skill, read from the bundle
// Core stored: what an agent is given, which is not always what the source
// holds today.
func (s *Service) SkillFiles(ctx context.Context, identity auth.Identity, id domain.SkillID) ([]skills.File, error) {
	skill, err := s.store.GetSkill(ctx, identity.UserID, id)
	if err != nil {
		return nil, translate(err)
	}
	_, body, err := s.OpenArtifact(ctx, identity, skill.ArtifactID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = body.Close() }()

	files, err := skills.Files(body, skillTextLimit)
	if err != nil {
		// What Core stored is unreadable: a fault here, not in the request.
		return nil, err
	}
	return files, nil
}

// UninstallSkill removes a Skill. The bundle it referenced goes with it: nothing
// else can point at it, because a Skill owns its own Artifact.
func (s *Service) UninstallSkill(ctx context.Context, identity auth.Identity, id domain.SkillID) error {
	skill, err := s.store.GetSkill(ctx, identity.UserID, id)
	if err != nil {
		return translate(err)
	}
	b := &batch{ownerID: identity.UserID}
	err = s.store.WithTx(ctx, func(tx *postgres.Store) error {
		if err := lockSkills(ctx, tx, identity.UserID, skill.ProjectID); err != nil {
			return err
		}
		var err error
		skill, err = tx.GetSkill(ctx, identity.UserID, id)
		if err != nil {
			return err
		}
		if _, err = tx.DeleteSkill(ctx, identity.UserID, id); err != nil {
			return err
		}
		payload := map[string]any{"skillId": skill.ID, "name": skill.Name, "installedRevision": skill.InstalledRevision}
		return s.recordSkillChange(ctx, tx, b, identity, skill, events.TypeSkillRemoved, payload)
	})
	if err != nil {
		return translate(err)
	}
	s.flush(b)
	if err := s.DeleteArtifact(ctx, identity, skill.ArtifactID); err != nil && !errors.Is(err, ErrNotFound) {
		s.logger.Error("cannot remove the bundle of an uninstalled skill",
			"artifactId", skill.ArtifactID, "error", err)
	}
	return nil
}

func (s *Service) recordSkillChange(ctx context.Context, tx *postgres.Store, b *batch, identity auth.Identity, skill domain.Skill, kind events.Type, payload map[string]any) error {
	scope := domain.Scope{ProjectID: skill.ProjectID}
	if err := s.recordLifecycleAudit(ctx, tx, identity, kind, scope, string(skill.ID), payload); err != nil {
		return err
	}
	return s.appendAll(ctx, tx, b, record{eventType: kind, scope: scope, payload: payload})
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
	_, err := s.withBackendReport(ctx, backendID, func(tx *postgres.Store, _ domain.BackendInstance) error {
		return tx.ReplaceBackendSkills(ctx, backendID, local)
	})
	return translate(err)
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
