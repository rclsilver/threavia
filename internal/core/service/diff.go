package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"

	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/events"
)

// diffTimeout bounds how long a person waits for a backend to compute a diff.
const diffTimeout = 30 * time.Second

// FileDiff is the change a Job made to one file, as the backend computed it.
type FileDiff struct {
	Path      string `json:"path"`
	Diff      string `json:"diff"`
	Binary    bool   `json:"binary"`
	Truncated bool   `json:"truncated"`
}

// FileDiffOf asks the backend that ran a Job for the diff of one file it
// changed (spec section 22).
//
// Core never keeps a diff: it is computed on the machine that holds the
// working directory, when someone opens it, from the two trees the change
// summary named. A backend that is away cannot answer, and neither can one
// whose git collected those trees; both are said rather than papered over.
func (s *Service) FileDiffOf(ctx context.Context, identity auth.Identity, sessionID domain.SessionID, sequence domain.Sequence, path string) (FileDiff, error) {
	event, err := s.store.SessionEvent(ctx, identity.UserID, sessionID, sequence)
	if err != nil {
		return FileDiff{}, translate(err)
	}
	if event.Type != events.TypeWorkspaceChanged {
		return FileDiff{}, fmt.Errorf("%w: this event is not a change to the workspace", ErrInvalid)
	}
	var change WorkspaceChangedPayload
	if err := json.Unmarshal(event.Payload, &change); err != nil {
		return FileDiff{}, fmt.Errorf("read the change summary: %w", err)
	}
	listed := false
	for _, file := range change.Files {
		if file.Path == path {
			listed = true
			break
		}
	}
	// Only what the summary listed: the request goes to someone's machine,
	// and it is not a way to read any file of their repository.
	if !listed {
		return FileDiff{}, fmt.Errorf("%w: this file is not part of the change", ErrNotFound)
	}
	if change.BaseTree == "" || change.HeadTree == "" {
		return FileDiff{}, fmt.Errorf("%w: the backend did not record this change in a way it can show", ErrConflict)
	}

	if event.RunID == nil {
		return FileDiff{}, fmt.Errorf("%w: this change belongs to no run", ErrNotFound)
	}
	run, err := s.store.RunByID(ctx, *event.RunID)
	if err != nil {
		return FileDiff{}, translate(err)
	}
	conn, ok := s.backends.Lookup(run.BackendInstanceID)
	if !ok {
		return FileDiff{}, fmt.Errorf("%w: the backend holding this change is offline", ErrBackendUnavailable)
	}

	requestID := domain.NewUUID()
	answer := make(chan *backendv1.WorkspaceDiff, 1)
	s.diffs.Store(requestID, answer)
	defer s.diffs.Delete(requestID)

	conn.Send(&backendv1.CoreToBackend{
		Message: &backendv1.CoreToBackend_WorkspaceDiffRequest{WorkspaceDiffRequest: &backendv1.WorkspaceDiffRequest{
			RequestId: requestID,
			Directory: change.Directory,
			BaseTree:  change.BaseTree,
			HeadTree:  change.HeadTree,
			Path:      path,
		}},
	})

	select {
	case diff := <-answer:
		if diff.GetError() != nil {
			return FileDiff{}, fmt.Errorf("%w: %s", ErrConflict, diff.GetError().GetMessage())
		}
		return FileDiff{Path: path, Diff: diff.GetDiff(), Binary: diff.GetBinary(), Truncated: diff.GetTruncated()}, nil
	case <-time.After(diffTimeout):
		return FileDiff{}, fmt.Errorf("%w: the backend did not answer in time", ErrBackendUnavailable)
	case <-ctx.Done():
		return FileDiff{}, ctx.Err()
	}
}

// WorkspaceDiff implements the backendconn Sink: it hands an answer to the
// request waiting for it. One nobody waits for any more is dropped.
func (s *Service) WorkspaceDiff(_ context.Context, _ domain.BackendInstanceID, diff *backendv1.WorkspaceDiff) {
	waiting, ok := s.diffs.Load(diff.GetRequestId())
	if !ok {
		return
	}
	select {
	case waiting.(chan *backendv1.WorkspaceDiff) <- diff:
	default:
	}
}
