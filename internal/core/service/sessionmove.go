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
//
// With withKnowledge, the tasks and decisions agents created from the Session's
// Jobs follow it, except those linked to ones that stay behind.
func (s *Service) MoveSessionToProject(ctx context.Context, identity auth.Identity, sessionID domain.SessionID, projectID domain.ProjectID, withKnowledge bool) (domain.Session, MoveSummary, error) {
	var (
		result  domain.Session
		summary MoveSummary
	)
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
		if withKnowledge {
			if summary, err = moveKnowledge(ctx, tx, identity, sessionID, before.ProjectID, projectID); err != nil {
				return err
			}
		}
		scope := domain.Scope{ProjectID: projectID, SessionID: sessionID}
		return s.recordAuditedChange(ctx, tx, b, identity, events.TypeSessionProjectMoved, scope, string(sessionID),
			SessionProjectMovedPayload{FromProjectID: before.ProjectID, ToProjectID: projectID, WorkingDirectoryCleared: cleared, Knowledge: summary})
	})
	if err != nil {
		return domain.Session{}, MoveSummary{}, translate(err)
	}
	s.flush(b)
	return result, summary, nil
}

// MoveSummary says what followed a moved Session, and what stayed behind
// because it is linked to something that did.
type MoveSummary struct {
	MovedTasks     []string `json:"movedTasks,omitempty"`
	LeftTasks      []string `json:"leftTasks,omitempty"`
	MovedDecisions []string `json:"movedDecisions,omitempty"`
	LeftDecisions  []string `json:"leftDecisions,omitempty"`
}

func moveKnowledge(ctx context.Context, tx *postgres.Store, identity auth.Identity, sessionID domain.SessionID, from, to domain.ProjectID) (MoveSummary, error) {
	tasks, decisions, err := tx.SessionKnowledge(ctx, identity.UserID, sessionID, from)
	if err != nil {
		return MoveSummary{}, err
	}
	taskEdges, decisionEdges, err := tx.KnowledgeEdges(ctx, tasks, decisions)
	if err != nil {
		return MoveSummary{}, err
	}
	var summary MoveSummary
	summary.MovedTasks, summary.LeftTasks = closedUnderLinks(tasks, taskEdges)
	summary.MovedDecisions, summary.LeftDecisions = closedUnderLinks(decisions, decisionEdges)
	return summary, tx.MoveKnowledge(ctx, to, summary.MovedTasks, summary.MovedDecisions)
}

// closedUnderLinks splits candidates into those that can move and those that
// cannot: an item stays when it is linked to one that stays, or that is not a
// candidate, since a link must never join two Projects.
func closedUnderLinks(candidates []string, edges []postgres.Edge) (moved, left []string) {
	keep := make(map[string]bool, len(candidates))
	for _, id := range candidates {
		keep[id] = true
	}
	for changed := true; changed; {
		changed = false
		for _, edge := range edges {
			if keep[edge.From] != keep[edge.To] {
				keep[edge.From], keep[edge.To] = false, false
				changed = true
			}
		}
	}
	for _, id := range candidates {
		if keep[id] {
			moved = append(moved, id)
		} else {
			left = append(left, id)
		}
	}
	return moved, left
}
