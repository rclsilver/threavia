package service

import (
	"context"
	"fmt"
	"time"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"

	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
)

// filesTimeout bounds how long a person typing @ waits for a backend: past it,
// the suggestions are not worth having any more.
const filesTimeout = 10 * time.Second

// WorkspaceFiles are the files of a Session's working directory that match
// what a person typed, as the backend holding it listed them just now.
type WorkspaceFiles struct {
	Directory string   `json:"directory"`
	Paths     []string `json:"paths"`
	Truncated bool     `json:"truncated"`
}

// FilesOf asks the backend of a Session's current Run for the files of its
// working directory whose path matches a query, best match first.
//
// Nothing is kept, and nothing but names is read: this is what lets a person
// name a file in a message without knowing its exact path.
func (s *Service) FilesOf(ctx context.Context, identity auth.Identity, sessionID domain.SessionID, query string, limit int) (WorkspaceFiles, error) {
	place, err := s.store.SessionPlace(ctx, identity.UserID, sessionID)
	if err != nil {
		return WorkspaceFiles{}, translate(err)
	}
	if place.KnownDirectoryName != nil && place.Path == nil {
		return WorkspaceFiles{}, fmt.Errorf("%w: %s is not located on this backend yet; it is once a job runs there",
			ErrConflict, *place.KnownDirectoryName)
	}
	backend, err := s.store.BackendInstanceByID(ctx, place.BackendInstanceID)
	if err != nil {
		return WorkspaceFiles{}, translate(err)
	}
	if !backend.HasFeature(domain.FeatureWorkspaceFiles) {
		return WorkspaceFiles{}, fmt.Errorf("%w: this backend cannot list files; update it", ErrConflict)
	}
	conn, ok := s.backends.Lookup(place.BackendInstanceID)
	if !ok {
		return WorkspaceFiles{}, fmt.Errorf("%w: the backend holding this session is offline", ErrBackendUnavailable)
	}

	requestID := domain.NewUUID()
	answer := make(chan *backendv1.WorkspaceFiles, 1)
	s.workspaceFiles.Store(requestID, answer)
	defer s.workspaceFiles.Delete(requestID)

	request := &backendv1.WorkspaceFilesRequest{RequestId: requestID, Query: query, Limit: uint32(max(limit, 0))}
	if place.Path != nil {
		request.Directory = *place.Path
	}
	if !conn.Send(&backendv1.CoreToBackend{
		Message: &backendv1.CoreToBackend_WorkspaceFilesRequest{WorkspaceFilesRequest: request},
	}) {
		return WorkspaceFiles{}, fmt.Errorf("%w: the backend holding this session went away", ErrBackendUnavailable)
	}

	select {
	case files := <-answer:
		if files.GetError() != nil {
			return WorkspaceFiles{}, fmt.Errorf("%w: %s", ErrConflict, files.GetError().GetMessage())
		}
		paths := files.GetPaths()
		if paths == nil {
			paths = []string{}
		}
		return WorkspaceFiles{Directory: files.GetDirectory(), Paths: paths, Truncated: files.GetTruncated()}, nil
	case <-time.After(filesTimeout):
		return WorkspaceFiles{}, fmt.Errorf("%w: the backend did not answer in time", ErrBackendUnavailable)
	case <-ctx.Done():
		return WorkspaceFiles{}, ctx.Err()
	}
}

// WorkspaceFiles implements the backendconn Sink: it hands an answer to the
// request waiting for it. One nobody waits for any more is dropped.
func (s *Service) WorkspaceFiles(_ context.Context, _ domain.BackendInstanceID, files *backendv1.WorkspaceFiles) {
	waiting, ok := s.workspaceFiles.Load(files.GetRequestId())
	if !ok {
		return
	}
	select {
	case waiting.(chan *backendv1.WorkspaceFiles) <- files:
	default:
	}
}
