package service

import (
	"context"
	"fmt"
	"time"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"

	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
)

// How long a person waits for a backend to read a repository: a status is a
// local read, a fetch goes to the remote and is bounded on the backend first.
const (
	repositoryTimeout      = 15 * time.Second
	repositoryFetchTimeout = 45 * time.Second
)

// Repository is where a Session's working directory stands in git, as the
// backend holding it read it just now.
type Repository struct {
	Directory    string     `json:"directory"`
	Tracked      bool       `json:"tracked"`
	Branch       string     `json:"branch,omitempty"`
	Head         string     `json:"head,omitempty"`
	Upstream     string     `json:"upstream,omitempty"`
	Ahead        int32      `json:"ahead"`
	Behind       int32      `json:"behind"`
	UpstreamGone bool       `json:"upstreamGone"`
	Staged       int32      `json:"staged"`
	Unstaged     int32      `json:"unstaged"`
	Untracked    int32      `json:"untracked"`
	Conflicted   int32      `json:"conflicted"`
	FetchedAt    *time.Time `json:"fetchedAt,omitempty"`
	FetchError   string     `json:"fetchError,omitempty"`
	CheckedAt    time.Time  `json:"checkedAt"`
}

// RepositoryOf asks the backend of a Session's current Run where its working
// directory stands in git: branch, distance from the upstream, what is not
// committed.
//
// Nothing is kept: the repository lives on someone's machine and changes
// without telling Core, so it is read when someone looks. With fetch, the
// backend fetches the remote first, which is the only way to know whether the
// branch is behind; without it, the comparison is with the remote as last
// fetched, and the answer says when that was.
func (s *Service) RepositoryOf(ctx context.Context, identity auth.Identity, sessionID domain.SessionID, fetch bool) (Repository, error) {
	place, err := s.store.SessionPlace(ctx, identity.UserID, sessionID)
	if err != nil {
		return Repository{}, translate(err)
	}
	if place.KnownDirectoryName != nil && place.Path == nil {
		return Repository{}, fmt.Errorf("%w: %s is not located on this backend yet; it is once a job runs there",
			ErrConflict, *place.KnownDirectoryName)
	}
	conn, ok := s.backends.Lookup(place.BackendInstanceID)
	if !ok {
		return Repository{}, fmt.Errorf("%w: the backend holding this session is offline", ErrBackendUnavailable)
	}

	requestID := domain.NewUUID()
	answer := make(chan *backendv1.RepositoryStatus, 1)
	s.repositories.Store(requestID, answer)
	defer s.repositories.Delete(requestID)

	request := &backendv1.RepositoryStatusRequest{RequestId: requestID, Fetch: fetch}
	if place.Path != nil {
		request.Directory = *place.Path
	}
	if !conn.Send(&backendv1.CoreToBackend{
		Message: &backendv1.CoreToBackend_RepositoryStatusRequest{RepositoryStatusRequest: request},
	}) {
		return Repository{}, fmt.Errorf("%w: the backend holding this session went away", ErrBackendUnavailable)
	}

	timeout := repositoryTimeout
	if fetch {
		timeout = repositoryFetchTimeout
	}
	select {
	case status := <-answer:
		if status.GetError() != nil {
			return Repository{}, fmt.Errorf("%w: %s", ErrConflict, status.GetError().GetMessage())
		}
		repo := Repository{
			Directory:    status.GetDirectory(),
			Tracked:      status.GetTracked(),
			Branch:       status.GetBranch(),
			Head:         status.GetHead(),
			Upstream:     status.GetUpstream(),
			Ahead:        status.GetAhead(),
			Behind:       status.GetBehind(),
			UpstreamGone: status.GetUpstreamGone(),
			Staged:       status.GetStaged(),
			Unstaged:     status.GetUnstaged(),
			Untracked:    status.GetUntracked(),
			Conflicted:   status.GetConflicted(),
			FetchError:   status.GetFetchError(),
			CheckedAt:    s.now(),
		}
		if status.GetFetchedAt() != nil {
			at := status.GetFetchedAt().AsTime()
			repo.FetchedAt = &at
		}
		return repo, nil
	case <-time.After(timeout):
		return Repository{}, fmt.Errorf("%w: the backend did not answer in time", ErrBackendUnavailable)
	case <-ctx.Done():
		return Repository{}, ctx.Err()
	}
}

// RepositoryStatus implements the backendconn Sink: it hands an answer to the
// request waiting for it. One nobody waits for any more is dropped.
func (s *Service) RepositoryStatus(_ context.Context, _ domain.BackendInstanceID, status *backendv1.RepositoryStatus) {
	waiting, ok := s.repositories.Load(status.GetRequestId())
	if !ok {
		return
	}
	select {
	case waiting.(chan *backendv1.RepositoryStatus) <- status:
	default:
	}
}
