package service

import (
	"context"
	"fmt"

	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
)

// MakeDecisionGlobal preserves the fact and its project history. Agent calls
// are restricted to the calling Job's project by CreateDecision.
func (s *Service) MakeDecisionGlobal(ctx context.Context, identity auth.Identity, id domain.DecisionID, createdBy *domain.JobID) (domain.Decision, error) {
	previous, err := s.store.GetDecision(ctx, identity.UserID, id)
	if err != nil {
		return domain.Decision{}, translate(err)
	}
	if previous.Status != domain.DecisionActive {
		return domain.Decision{}, fmt.Errorf("%w: decision is already superseded", ErrConflict)
	}
	if previous.ProjectID == "" {
		return previous, nil
	}
	return s.CreateDecision(ctx, identity, "", previous.Title, previous.Content, previous.Importance, &id, createdBy)
}

func memoryProject(projectID domain.ProjectID, scope string) (domain.ProjectID, error) {
	switch scope {
	case "", "project":
		return projectID, nil
	case "global":
		return "", nil
	default:
		return "", fmt.Errorf("%w: unknown memory scope %q", ErrInvalid, scope)
	}
}
