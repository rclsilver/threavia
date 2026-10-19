package service

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/storage/postgres"
	"github.com/rclsilver/threavia/internal/core/tools"
	"slices"
	"strings"
)

func (s *Service) reviewTool(ctx context.Context, identity auth.Identity, scope jobScope, name tools.Name, input map[string]any) (map[string]any, error) {
	var review domain.Review
	var err error
	switch name {
	case tools.NameReviewRepositoryRegister:
		source := "DECLARED"
		if before, _ := input["beforeWork"].(bool); before {
			source = "BEFORE_WORK"
		}
		review, err = s.RegisterReviewRepository(ctx, identity, scope.SessionID, text(input, "directory"), text(input, "name"), source)
	case tools.NameReviewRead:
		review, err = s.ReadReview(ctx, identity, scope.SessionID)
	case tools.NameReviewRequest:
		review, err = s.ReadReview(ctx, identity, scope.SessionID)
		if err == nil {
			action := "refresh"
			if required, _ := input["required"].(bool); required {
				action = "require"
			}
			review, err = s.ActOnReview(ctx, identity, scope.SessionID, ReviewAction{Action: action, ExpectedVersion: review.Version})
		}
	case tools.NameReviewReply:
		body := strings.TrimSpace(text(input, "body"))
		if body == "" || len(body) > 20000 {
			return nil, ErrInvalid
		}
		review, err = s.editReview(ctx, identity, scope.SessionID, -1, func(_ *postgres.Store, r *domain.Review) error {
			idx := slices.IndexFunc(r.Threads, func(t domain.ReviewThread) bool { return t.ID == text(input, "threadId") })
			if idx < 0 {
				return ErrNotFound
			}
			published := false
			for _, m := range r.Threads[idx].Messages {
				if !m.Draft {
					published = true
				}
			}
			if !published {
				return ErrNotFound
			}
			r.Threads[idx].Messages = append(r.Threads[idx].Messages, domain.ReviewMessage{ID: domain.NewUUID(), Body: body, Author: "agent", CreatedAt: s.now()})
			return nil
		})
	default:
		return nil, fmt.Errorf("%w: unknown review tool", ErrInvalid)
	}
	if err != nil {
		return nil, err
	}
	// Drafts do not enter agent context until the human explicitly publishes.
	filtered := []domain.ReviewThread{}
	for _, thread := range review.Threads {
		messages := []domain.ReviewMessage{}
		for _, m := range thread.Messages {
			if !m.Draft {
				messages = append(messages, m)
			}
		}
		if len(messages) > 0 {
			thread.Messages = messages
			filtered = append(filtered, thread)
		}
	}
	review.Threads = filtered
	encoded, err := json.Marshal(review)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	err = json.Unmarshal(encoded, &result)
	return result, err
}
