package service

import (
	"context"
	"fmt"
	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
	"slices"
)

func reviewComparisonBase(r domain.Review, revisionIndex int, repo domain.ReviewRepository, compareTo string) (string, error) {
	if compareTo == "initial" {
		return repo.InitialTree, nil
	}
	previous := slices.IndexFunc(r.Revisions, func(v domain.ReviewRevision) bool { return v.ID == compareTo })
	if previous < 0 || previous >= revisionIndex {
		return "", fmt.Errorf("%w: choose an earlier revision", ErrInvalid)
	}
	for _, state := range r.Revisions[previous].Repositories {
		if state.RepositoryID == repo.ID && state.Error == "" {
			return state.HeadTree, nil
		}
	}
	return "", fmt.Errorf("%w: repository was not captured in the earlier revision", ErrConflict)
}

// ReviewComparison computes its own changed-file list, so reverted files and
// already-approved files do not disappear when expanding the review scope.
func (s *Service) ReviewComparison(ctx context.Context, _ auth.Identity, r domain.Review, revisionID, compareTo string) (domain.Review, error) {
	index := slices.IndexFunc(r.Revisions, func(v domain.ReviewRevision) bool { return v.ID == revisionID })
	if index < 0 {
		return r, ErrNotFound
	}
	for i, state := range r.Revisions[index].Repositories {
		repoIndex := slices.IndexFunc(r.Repositories, func(repo domain.ReviewRepository) bool { return repo.ID == state.RepositoryID })
		if repoIndex < 0 {
			return r, ErrNotFound
		}
		repo := r.Repositories[repoIndex]
		base, err := reviewComparisonBase(r, index, repo, compareTo)
		if err != nil {
			return r, err
		}
		var listing struct{ Files []domain.ReviewFile }
		if err = s.reviewRead(ctx, repo.BackendInstanceID, &backendv1.ReviewWorkspaceRequest{Operation: "LIST", Directory: repo.GitDirectory, BaseTree: base, HeadTree: state.HeadTree}, &listing); err != nil {
			return r, err
		}
		r.Revisions[index].Repositories[i].BaseTree = base
		r.Revisions[index].Repositories[i].Files = listing.Files
	}
	return r, nil
}
