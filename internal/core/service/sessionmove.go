package service

import (
	"context"
	"fmt"
	"slices"

	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/events"
	"github.com/rclsilver/threavia/internal/core/storage/postgres"
)

// SessionMove is the result of re-filing a Session under another Project.
type SessionMove struct {
	Session   domain.Session `json:"session"`
	Knowledge MoveSummary    `json:"knowledge"`
}

// MoveSummary says which tasks and decisions followed a moved Session, and
// which stayed behind because they are linked to something that did not.
type MoveSummary struct {
	MovedTasks     []string `json:"movedTasks"`
	LeftTasks      []string `json:"leftTasks"`
	MovedDecisions []string `json:"movedDecisions"`
	LeftDecisions  []string `json:"leftDecisions"`
}

// MoveSessionToProject re-files a Session under another Project of the same
// person, with its history, requests and files. Work in progress is not moved
// from under a running Job: finish or cancel it first. A working directory
// that belongs to the old Project is detached; a global one is kept.
//
// With withKnowledge, the tasks and decisions agents created from the Session's
// Jobs follow it, except those linked to ones that stay behind: a link never
// joins two Projects.
func (s *Service) MoveSessionToProject(ctx context.Context, identity auth.Identity, sessionID domain.SessionID, projectID domain.ProjectID, withKnowledge bool) (SessionMove, error) {
	if !isUUID(string(projectID)) {
		return SessionMove{}, fmt.Errorf("%w: projectId must be a project identifier", ErrInvalid)
	}
	result := SessionMove{Knowledge: emptySummary()}
	b := &batch{ownerID: identity.UserID}
	err := s.store.WithTx(ctx, func(tx *postgres.Store) error {
		current, err := tx.GetSession(ctx, identity.UserID, sessionID)
		if err != nil {
			return err
		}
		if current.ProjectID == projectID {
			result.Session = current
			return nil
		}
		// The order every knowledge mutation takes: decision memory, then the
		// Projects, then the Session. Projects in a fixed order, so two moves in
		// opposite directions cannot deadlock.
		if withKnowledge {
			if err := tx.LockDecisionMemory(ctx, identity.UserID); err != nil {
				return err
			}
		}
		projects := []domain.ProjectID{current.ProjectID, projectID}
		slices.Sort(projects)
		var target domain.Project
		for _, id := range projects {
			project, err := tx.LockProject(ctx, identity.UserID, id)
			if err != nil {
				return err
			}
			if id == projectID {
				target = project
			}
		}
		if target.Status != domain.ProjectActive {
			return fmt.Errorf("%w: the destination project is archived", ErrInvalid)
		}
		before, err := tx.LockSession(ctx, identity.UserID, sessionID)
		if err != nil {
			return err
		}
		if before.ProjectID != current.ProjectID {
			return fmt.Errorf("%w: the session was moved meanwhile", ErrConflict)
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
		result.Session, err = tx.MoveSessionToProject(ctx, identity.UserID, sessionID, projectID, dir)
		if err != nil {
			return err
		}
		if withKnowledge {
			if result.Knowledge, err = moveKnowledge(ctx, tx, identity, sessionID, before.ProjectID, projectID); err != nil {
				return err
			}
		}
		scope := domain.Scope{ProjectID: projectID, SessionID: sessionID}
		return s.recordAuditedChange(ctx, tx, b, identity, events.TypeSessionProjectMoved, scope, string(sessionID),
			SessionProjectMovedPayload{FromProjectID: before.ProjectID, ToProjectID: projectID, WorkingDirectoryCleared: cleared, Knowledge: result.Knowledge})
	})
	if err != nil {
		return SessionMove{}, translate(err)
	}
	s.flush(b)
	return result, nil
}

func emptySummary() MoveSummary {
	return MoveSummary{MovedTasks: []string{}, LeftTasks: []string{}, MovedDecisions: []string{}, LeftDecisions: []string{}}
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
	summary := emptySummary()
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
	moved, left = []string{}, []string{}
	for _, id := range candidates {
		if keep[id] {
			moved = append(moved, id)
		} else {
			left = append(left, id)
		}
	}
	return moved, left
}
