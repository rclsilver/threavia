package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/events"
	"github.com/rclsilver/threavia/internal/core/storage/postgres"
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
// section 24). The Session and the Job it is attached to are checked like the
// Project, before any byte is written.
func (s *Service) UploadArtifact(ctx context.Context, identity auth.Identity, projectID domain.ProjectID, filename, mimeType string, body io.Reader, scope domain.Scope) (domain.Artifact, error) {
	if s.objects == nil {
		return domain.Artifact{}, ErrStorageUnavailable
	}
	if _, err := s.store.GetProject(ctx, identity.UserID, projectID); err != nil {
		return domain.Artifact{}, translate(err)
	}
	scope, err := s.artifactScope(ctx, identity, projectID, scope)
	if err != nil {
		return domain.Artifact{}, err
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

// errArtifactScope is the one answer to a Session or Job an upload cannot be
// attached to. It does not say whether the id exists: a missing one and
// another user's must read the same, or the upload becomes a way to probe for
// identifiers.
var errArtifactScope = fmt.Errorf("%w: the session or job given is not one of this project", ErrInvalid)

// artifactScope checks the Session and the Job an upload names against the
// caller and the Project, and returns the scope to record.
//
// A Job given without its Session gets it filled in from the Job: the Session
// is how Artifacts are listed, and one recorded against a Job alone would be
// missing from the Session it was made in.
func (s *Service) artifactScope(ctx context.Context, identity auth.Identity, projectID domain.ProjectID, scope domain.Scope) (domain.Scope, error) {
	if scope.JobID != "" {
		if !isUUID(string(scope.JobID)) {
			return domain.Scope{}, errArtifactScope
		}
		job, err := s.store.GetJob(ctx, identity.UserID, scope.JobID)
		if err != nil {
			return domain.Scope{}, scopeError(err)
		}
		run, err := s.store.GetRun(ctx, identity.UserID, job.RunID)
		if err != nil {
			return domain.Scope{}, scopeError(err)
		}
		if scope.SessionID == "" {
			scope.SessionID = run.SessionID
		} else if scope.SessionID != run.SessionID {
			return domain.Scope{}, errArtifactScope
		}
	}

	if scope.SessionID != "" {
		if !isUUID(string(scope.SessionID)) {
			return domain.Scope{}, errArtifactScope
		}
		session, err := s.store.GetSession(ctx, identity.UserID, scope.SessionID)
		if err != nil {
			return domain.Scope{}, scopeError(err)
		}
		if session.ProjectID != projectID {
			return domain.Scope{}, errArtifactScope
		}
	}
	return scope, nil
}

// scopeError keeps a missing Session or Job indistinguishable from a foreign
// one, and anything else a failure of Core rather than of the request.
func scopeError(err error) error {
	if errors.Is(err, postgres.ErrNotFound) {
		return errArtifactScope
	}
	return translate(err)
}

// isUUID tells an identifier the database can look up from one it would fail
// to parse, which would otherwise surface as an internal error.
func isUUID(value string) bool {
	_, err := uuid.Parse(value)
	return err == nil
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

// ListArtifacts returns the Artifacts of a Project, or of one of its Sessions
// when sessionID is set.
func (s *Service) ListArtifacts(ctx context.Context, identity auth.Identity, projectID domain.ProjectID, sessionID *domain.SessionID, limit int) ([]domain.Artifact, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	artifacts, err := s.store.ListArtifacts(ctx, identity.UserID, projectID, sessionID, limit)
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

// maxPublishedBytes bounds a file an agent publishes. It travels inside one
// message of the control stream, whose limit is larger; anything bigger than a
// report or a screenshot belongs in the repository the agent works in.
const maxPublishedBytes = 10 << 20

// ArtifactPayload is what an artifact.created event says in a Session.
type ArtifactPayload struct {
	ArtifactID string `json:"artifactId"`
	Filename   string `json:"filename"`
	MimeType   string `json:"mimeType"`
	Size       int64  `json:"size"`
	Title      string `json:"title,omitempty"`
}

// publishArtifact stores a file an agent made and says so in its Session.
//
// The bytes come from the backend, which read the file the agent named on its
// own machine, within the Job's directories and the Job's policy. Core only
// sees the content: where the file was is the backend's business.
func (s *Service) publishArtifact(ctx context.Context, identity auth.Identity, jc jobScope, input map[string]any, file []byte) (map[string]any, error) {
	if len(file) == 0 {
		return nil, fmt.Errorf("%w: no file content arrived; the file is empty, or this backend cannot send files", ErrInvalid)
	}
	if len(file) > maxPublishedBytes {
		return nil, fmt.Errorf("%w: the file is %d bytes, more than the %d a published artifact may be", ErrInvalid, len(file), maxPublishedBytes)
	}

	filename := text(input, "filename")
	if filename == "" {
		filename = filepath.Base(text(input, "path"))
	}
	mimeType := mime.TypeByExtension(strings.ToLower(filepath.Ext(filename)))
	if mimeType == "" {
		mimeType = http.DetectContentType(file)
	}

	scope := domain.Scope{ProjectID: jc.ProjectID, SessionID: jc.SessionID, JobID: jc.JobID}
	artifact, err := s.UploadArtifact(ctx, identity, jc.ProjectID, filename, mimeType, bytes.NewReader(file), scope)
	if err != nil {
		return nil, err
	}

	s.emit(ctx, identity.UserID, events.TypeArtifactCreated, scope, ArtifactPayload{
		ArtifactID: string(artifact.ID),
		Filename:   artifact.Filename,
		MimeType:   artifact.MimeType,
		Size:       artifact.Size,
		Title:      text(input, "title"),
	})
	return map[string]any{
		"artifactId": string(artifact.ID),
		"filename":   artifact.Filename,
		"size":       artifact.Size,
		"published":  "It is in the conversation now, and kept with the project's artifacts.",
	}, nil
}
