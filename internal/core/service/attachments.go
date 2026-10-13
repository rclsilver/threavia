package service

import (
	"context"
	"errors"
	"fmt"
	"io"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/storage/postgres"
)

// Kept below the default gRPC receive limit, including the project context.
const maxPromptAttachmentBytes = 3 << 20

// Replay before looking up files: a successful send remains successful even
// if its attachments were removed before the client received the response.
func (s *Service) replayAttachmentMessage(ctx context.Context, identity auth.Identity, sessionID domain.SessionID, key string) (domain.Job, bool, error) {
	if key == "" {
		return domain.Job{}, false, nil
	}
	job, err := s.store.JobByIdempotencyKey(ctx, identity.UserID, key)
	if errors.Is(err, postgres.ErrNotFound) {
		return domain.Job{}, false, nil
	}
	if err != nil {
		return domain.Job{}, false, translate(err)
	}
	run, err := s.store.GetRun(ctx, identity.UserID, job.RunID)
	if err != nil {
		return domain.Job{}, false, translate(err)
	}
	if run.SessionID != sessionID {
		return domain.Job{}, false, fmt.Errorf("%w: this Idempotency-Key was already used for a message in another session", ErrConflict)
	}
	return job, true, nil
}

func (s *Service) checkPromptAttachmentsBackend(ctx context.Context, backendID domain.BackendInstanceID) error {
	backend, err := s.store.BackendInstanceByID(ctx, backendID)
	if err != nil {
		return translate(err)
	}
	if !backend.HasFeature(domain.FeaturePromptAttachments) {
		return fmt.Errorf("%w: update and reconnect this backend to send attachments", ErrConflict)
	}
	return nil
}

func (s *Service) promptAttachments(ctx context.Context, identity auth.Identity, projectID domain.ProjectID, ids []domain.ArtifactID) ([]ArtifactPayload, error) {
	if len(ids) > 8 {
		return nil, fmt.Errorf("%w: at most 8 attachments per message", ErrInvalid)
	}
	var refs []ArtifactPayload
	var total int64
	seen := make(map[domain.ArtifactID]bool)
	for _, id := range ids {
		if seen[id] {
			return nil, fmt.Errorf("%w: duplicate attachment", ErrInvalid)
		}
		seen[id] = true
		artifact, err := s.GetArtifact(ctx, identity, id)
		if err != nil {
			return nil, err
		}
		if artifact.ProjectID != projectID {
			return nil, fmt.Errorf("%w: attachment must belong to this project", ErrInvalid)
		}
		total += artifact.Size
		if total > maxPromptAttachmentBytes {
			return nil, fmt.Errorf("%w: attachments exceed 3 MiB per message", ErrInvalid)
		}
		refs = append(refs, ArtifactPayload{ArtifactID: string(id), Filename: artifact.Filename, MimeType: artifact.MimeType, Size: artifact.Size})
	}
	return refs, nil
}

func (s *Service) loadPromptAttachments(ctx context.Context, identity auth.Identity, projectID domain.ProjectID, ids []domain.ArtifactID) ([]*backendv1.PromptAttachment, error) {
	if _, err := s.promptAttachments(ctx, identity, projectID, ids); err != nil {
		return nil, err
	}
	var out []*backendv1.PromptAttachment
	var total int
	for _, id := range ids {
		artifact, body, err := s.OpenArtifact(ctx, identity, id)
		if err != nil {
			return nil, err
		}
		content, err := io.ReadAll(io.LimitReader(body, int64(maxPromptAttachmentBytes-total+1)))
		_ = body.Close()
		if err != nil {
			return nil, err
		}
		total += len(content)
		if total > maxPromptAttachmentBytes {
			return nil, fmt.Errorf("%w: attachments exceed 3 MiB", ErrInvalid)
		}
		out = append(out, &backendv1.PromptAttachment{ArtifactId: string(id), Filename: artifact.Filename, MimeType: artifact.MimeType, Content: content})
	}
	return out, nil
}
