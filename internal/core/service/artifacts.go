package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/storage/s3"
)

// ErrStorageUnavailable is returned when object storage is required but not
// configured. It is a distinct error because the fix is a deployment change, not
// a different request.
var ErrStorageUnavailable = errors.New("object storage is not available")

// SetObjectStore installs the object storage backing Artifacts. Without one,
// every Artifact operation refuses rather than pretending to work.
func (s *Service) SetObjectStore(store s3.ObjectStore, maxUploadBytes int64) {
	s.objects = store
	s.maxArtifactBytes = maxUploadBytes
}

// MaxArtifactBytes is the configured upload bound, so the HTTP layer can refuse
// an oversized body before it travels through object storage.
func (s *Service) MaxArtifactBytes() int64 { return s.maxArtifactBytes }

// UploadArtifact stores a blob and records its metadata.
//
// The checksum is computed from what was actually written, so a truncated
// upload cannot be recorded as a good one, and the object key is derived from
// identifiers rather than from the filename a caller supplied (spec
// section 24).
func (s *Service) UploadArtifact(ctx context.Context, identity auth.Identity, projectID domain.ProjectID, filename, mimeType string, body io.Reader, scope domain.Scope) (domain.Artifact, error) {
	if s.objects == nil {
		return domain.Artifact{}, ErrStorageUnavailable
	}
	if _, err := s.store.GetProject(ctx, identity.UserID, projectID); err != nil {
		return domain.Artifact{}, translate(err)
	}

	filename = sanitiseFilename(filename)
	if filename == "" {
		return domain.Artifact{}, fmt.Errorf("%w: a filename is required", ErrInvalid)
	}

	artifact := domain.Artifact{
		ID:        domain.NewArtifactID(),
		OwnerID:   identity.UserID,
		ProjectID: projectID,
		Filename:  filename,
		MimeType:  mimeType,
	}
	artifact.ObjectKey = s3.ObjectKey(string(projectID), string(artifact.ID))
	if scope.SessionID != "" {
		artifact.SessionID = &scope.SessionID
	}
	if scope.JobID != "" {
		artifact.JobID = &scope.JobID
	}

	stored, err := s.objects.Put(ctx, artifact.ObjectKey, body, s3.ObjectMeta{ContentType: mimeType})
	if err != nil {
		return domain.Artifact{}, fmt.Errorf("%w: %s", ErrInvalid, err)
	}
	artifact.Size = stored.Size
	artifact.SHA256 = stored.SHA256
	artifact.MimeType = stored.ContentType

	if err := s.store.CreateArtifact(ctx, &artifact); err != nil {
		// The bytes are already there; leaving them orphaned would be worse than
		// the failed request.
		if removeErr := s.objects.Delete(ctx, artifact.ObjectKey); removeErr != nil {
			s.logger.Error("cannot remove the bytes of a failed upload",
				"objectKey", artifact.ObjectKey, "error", removeErr)
		}
		return domain.Artifact{}, translate(err)
	}
	return artifact, nil
}

// OpenArtifact returns the metadata and the bytes of an Artifact.
func (s *Service) OpenArtifact(ctx context.Context, identity auth.Identity, id domain.ArtifactID) (domain.Artifact, io.ReadCloser, error) {
	artifact, err := s.store.GetArtifact(ctx, identity.UserID, id)
	if err != nil {
		return domain.Artifact{}, nil, translate(err)
	}
	if s.objects == nil {
		return artifact, nil, ErrStorageUnavailable
	}

	body, _, err := s.objects.Get(ctx, artifact.ObjectKey)
	if err != nil {
		if errors.Is(err, s3.ErrObjectNotFound) {
			// Metadata without bytes is a real state, and saying so beats a
			// generic failure: it points at the storage, not at the request.
			return artifact, nil, fmt.Errorf("%w: the stored object is missing", ErrNotFound)
		}
		return artifact, nil, err
	}
	return artifact, body, nil
}

// GetArtifact returns the metadata of an Artifact.
func (s *Service) GetArtifact(ctx context.Context, identity auth.Identity, id domain.ArtifactID) (domain.Artifact, error) {
	artifact, err := s.store.GetArtifact(ctx, identity.UserID, id)
	return artifact, translate(err)
}

// ListArtifacts returns the Artifacts of a Project.
func (s *Service) ListArtifacts(ctx context.Context, identity auth.Identity, projectID domain.ProjectID, limit int) ([]domain.Artifact, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	artifacts, err := s.store.ListArtifacts(ctx, identity.UserID, projectID, limit)
	return artifacts, translate(err)
}

// DeleteArtifact removes an Artifact and the bytes behind it.
func (s *Service) DeleteArtifact(ctx context.Context, identity auth.Identity, id domain.ArtifactID) error {
	key, err := s.store.DeleteArtifact(ctx, identity.UserID, id)
	if err != nil {
		return translate(err)
	}
	if s.objects == nil {
		return nil
	}
	if err := s.objects.Delete(ctx, key); err != nil && !errors.Is(err, s3.ErrObjectNotFound) {
		// The metadata is gone, so the Artifact is gone as far as a client is
		// concerned; an orphaned object is a storage problem, not a failed
		// request.
		s.logger.Error("cannot remove the bytes of a deleted artifact",
			"objectKey", key, "error", err)
	}
	return nil
}

// sanitiseFilename keeps a filename usable as a label without letting it carry a
// path. It is metadata; the object key never derives from it.
func sanitiseFilename(name string) string {
	name = strings.TrimSpace(filepath.Base(strings.TrimSpace(name)))
	if name == "." || name == ".." || name == string(filepath.Separator) {
		return ""
	}
	return name
}
