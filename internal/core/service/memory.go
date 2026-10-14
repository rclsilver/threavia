package service

import (
	"fmt"

	"github.com/rclsilver/threavia/internal/core/domain"
)

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
