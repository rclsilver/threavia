package service

import (
	"context"
	"fmt"

	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/events"
	"github.com/rclsilver/threavia/internal/core/storage/postgres"
)

// MoveSessionToProject re-files a Session under another Project of the same
// person, with its history and files. Work in progress is not moved from under
// a running Job: finish or cancel it first. A working directory that belongs to
// the old Project is detached; a global one is kept.
func (s *Service) MoveSessionToProject(ctx context.Context, identity auth.Identity, sessionID domain.SessionID, projectID domain.ProjectID) (domain.Session, error) {
	var result domain.Session
	b := &batch{ownerID: identity.UserID}
	err := s.store.WithTx(ctx, func(tx *postgres.Store) error {
		before, err := tx.LockSession(ctx, identity.UserID, sessionID)
		if err != nil {
			return err
		}
		if before.ProjectID == projectID {
			result = before
			return nil
		}
		target, err := tx.GetProject(ctx, identity.UserID, projectID)
		if err != nil {
			return err
		}
		if target.Status != domain.ProjectActive {
			return fmt.Errorf("%w: the destination project is archived", ErrInvalid)
		}
		jobs, err := tx.SessionToolJobs(ctx, sessionID)
		if err != nil {
			return err
		}
		for _, job := range jobs {
			if !job.Status.Terminal() {
				return fmt.Errorf("%w: finish or cancel the session's jobs before moving it", ErrConflict)
			}
		}
		dir, cleared := before.WorkingDirectoryID, false
		if dir != nil {
			known, err := tx.GetKnownDirectory(ctx, identity.UserID, domain.KnownDirectoryID(*dir))
			if err != nil {
				return err
			}
			if known.ProjectID != "" && known.ProjectID != projectID {
				dir, cleared = nil, true
			}
		}
		result, err = tx.MoveSessionToProject(ctx, identity.UserID, sessionID, projectID, dir)
		if err != nil {
			return err
		}
		scope := domain.Scope{ProjectID: projectID, SessionID: sessionID}
		return s.recordAuditedChange(ctx, tx, b, identity, events.TypeSessionProjectMoved, scope, string(sessionID),
			SessionProjectMovedPayload{FromProjectID: before.ProjectID, ToProjectID: projectID, WorkingDirectoryCleared: cleared})
	})
	if err != nil {
		return domain.Session{}, translate(err)
	}
	s.flush(b)
	return result, nil
}
