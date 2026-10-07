package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/events"
	"github.com/rclsilver/threavia/internal/core/storage/postgres"
)

// Bounds on what a search returns, so a tool result stays something an agent can
// actually read.
const (
	defaultSearchLimit = 20
	maxSearchLimit     = 100
)

// CreateTask files a Task, with its dependencies.
func (s *Service) CreateTask(ctx context.Context, identity auth.Identity, projectID domain.ProjectID, title, description string, dependsOn []domain.TaskID) (domain.Task, error) {
	if _, err := s.store.GetProject(ctx, identity.UserID, projectID); err != nil {
		return domain.Task{}, translate(err)
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return domain.Task{}, fmt.Errorf("%w: a task needs a title", ErrInvalid)
	}

	task := domain.Task{
		ID:          domain.NewTaskID(),
		ProjectID:   projectID,
		Title:       title,
		Description: strings.TrimSpace(description),
		Status:      domain.TaskTodo,
	}

	err := s.store.WithTx(ctx, func(tx *postgres.Store) error {
		if err := tx.CreateTask(ctx, &task); err != nil {
			return err
		}
		for _, dependency := range dependsOn {
			// Dependencies cross-check the project: a task cannot wait on work
			// filed somewhere else.
			blocker, err := tx.TaskByID(ctx, dependency)
			if err != nil {
				return fmt.Errorf("%w: unknown dependency %s", ErrInvalid, dependency)
			}
			if blocker.ProjectID != projectID {
				return fmt.Errorf("%w: dependency %s belongs to another project", ErrInvalid, dependency)
			}
			if err := tx.AddTaskDependency(ctx, task.ID, dependency); err != nil {
				return err
			}
			task.DependsOn = append(task.DependsOn, dependency)
		}
		return nil
	})
	if err != nil {
		return domain.Task{}, translateKnowledge(err)
	}

	s.emit(ctx, identity.UserID, events.TypeTaskCreated,
		domain.Scope{ProjectID: projectID}, TaskPayload{
			TaskID: string(task.ID), Title: task.Title, Status: task.Status.String(),
		})
	return task, nil
}

// UpdateTask changes a Task. Empty fields are left alone, so a status change
// does not have to resend the prose.
func (s *Service) UpdateTask(ctx context.Context, identity auth.Identity, id domain.TaskID, title, description string, status domain.TaskStatus) (domain.Task, error) {
	current, err := s.store.GetTask(ctx, identity.UserID, id)
	if err != nil {
		return domain.Task{}, translate(err)
	}
	if status != "" {
		if !status.Valid() {
			return domain.Task{}, fmt.Errorf("%w: unknown task status %q", ErrInvalid, status)
		}
		if status != current.Status && !current.Status.CanTransition(status) {
			return domain.Task{}, fmt.Errorf("%w: a task cannot go from %s to %s", ErrConflict, current.Status, status)
		}
	}

	task, err := s.store.UpdateTask(ctx, id, strings.TrimSpace(title), strings.TrimSpace(description), status)
	if err != nil {
		return domain.Task{}, translate(err)
	}

	s.emit(ctx, identity.UserID, events.TypeTaskUpdated,
		domain.Scope{ProjectID: task.ProjectID}, TaskPayload{
			TaskID: string(task.ID), Title: task.Title, Status: task.Status.String(),
		})
	return task, nil
}

// DeleteTask removes a Task for good.
//
// Filing one is cheap and an agent files them freely, so taking one back has to
// be cheap too, or a Project's list of what is left to do fills with work
// nobody ever meant to do. What was done about it stays in the timeline, which
// is where history lives; this only drops the entry from the list.
func (s *Service) DeleteTask(ctx context.Context, identity auth.Identity, id domain.TaskID) error {
	task, err := s.store.GetTask(ctx, identity.UserID, id)
	if err != nil {
		return translate(err)
	}
	if err := s.store.DeleteTask(ctx, identity.UserID, id); err != nil {
		return translate(err)
	}

	s.emit(ctx, identity.UserID, events.TypeTaskDeleted,
		domain.Scope{ProjectID: task.ProjectID}, TaskPayload{
			TaskID: string(task.ID), Title: task.Title, Status: task.Status.String(),
		})
	return nil
}

// AddTaskDependency records that a Task waits on another.
//
// The two must belong to the same Project, and the edge must not close a loop:
// nothing on a cycle is ever ready, so every Task on it would wait for good.
func (s *Service) AddTaskDependency(ctx context.Context, identity auth.Identity, id, dependsOn domain.TaskID) (domain.Task, error) {
	task, err := s.store.GetTask(ctx, identity.UserID, id)
	if err != nil {
		return domain.Task{}, translate(err)
	}
	blocker, err := s.store.GetTask(ctx, identity.UserID, dependsOn)
	if err != nil {
		return domain.Task{}, fmt.Errorf("%w: unknown dependency %s", ErrInvalid, dependsOn)
	}
	if blocker.ProjectID != task.ProjectID {
		return domain.Task{}, fmt.Errorf("%w: dependency %s belongs to another project", ErrInvalid, dependsOn)
	}
	if err := s.store.AddTaskDependency(ctx, id, dependsOn); err != nil {
		return domain.Task{}, translateKnowledge(err)
	}
	return s.taskChanged(ctx, identity, id)
}

// RemoveTaskDependency stops a Task from waiting on another.
func (s *Service) RemoveTaskDependency(ctx context.Context, identity auth.Identity, id, dependsOn domain.TaskID) (domain.Task, error) {
	if _, err := s.store.GetTask(ctx, identity.UserID, id); err != nil {
		return domain.Task{}, translate(err)
	}
	if err := s.store.RemoveTaskDependency(ctx, id, dependsOn); err != nil {
		return domain.Task{}, translate(err)
	}
	return s.taskChanged(ctx, identity, id)
}

// taskChanged re-reads a Task and announces it. The graph decides what is ready,
// so an edge changing is a change to the Task itself.
func (s *Service) taskChanged(ctx context.Context, identity auth.Identity, id domain.TaskID) (domain.Task, error) {
	task, err := s.store.GetTask(ctx, identity.UserID, id)
	if err != nil {
		return domain.Task{}, translate(err)
	}
	s.emit(ctx, identity.UserID, events.TypeTaskUpdated,
		domain.Scope{ProjectID: task.ProjectID}, TaskPayload{
			TaskID: string(task.ID), Title: task.Title, Status: task.Status.String(),
		})
	return task, nil
}

// ListTasks returns the Tasks of a Project.
func (s *Service) ListTasks(ctx context.Context, identity auth.Identity, projectID domain.ProjectID, includeDone bool) ([]domain.Task, error) {
	tasks, err := s.store.ListTasks(ctx, identity.UserID, projectID, includeDone)
	return tasks, translate(err)
}

// ReadyTasks returns the Tasks that can be started now: TODO, with every
// dependency done.
func (s *Service) ReadyTasks(ctx context.Context, identity auth.Identity, projectID domain.ProjectID, limit int) ([]domain.Task, error) {
	if _, err := s.store.GetProject(ctx, identity.UserID, projectID); err != nil {
		return nil, translate(err)
	}
	tasks, err := s.store.ReadyTasks(ctx, projectID, searchLimit(limit))
	return tasks, translate(err)
}

// SearchTasks finds Tasks by text.
func (s *Service) SearchTasks(ctx context.Context, identity auth.Identity, projectID domain.ProjectID, query string, limit int) ([]domain.Task, error) {
	if _, err := s.store.GetProject(ctx, identity.UserID, projectID); err != nil {
		return nil, translate(err)
	}
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("%w: a search needs a query", ErrInvalid)
	}
	tasks, err := s.store.SearchTasks(ctx, projectID, query, searchLimit(limit))
	return tasks, translate(err)
}

// CreateDecision records a Decision, superseding an earlier one when asked.
func (s *Service) CreateDecision(ctx context.Context, identity auth.Identity, projectID domain.ProjectID, title, content string, importance domain.DecisionImportance, supersedes *domain.DecisionID) (domain.Decision, error) {
	if _, err := s.store.GetProject(ctx, identity.UserID, projectID); err != nil {
		return domain.Decision{}, translate(err)
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return domain.Decision{}, fmt.Errorf("%w: a decision needs a title", ErrInvalid)
	}
	if importance == "" {
		importance = domain.DecisionNormal
	}
	if !importance.Valid() {
		return domain.Decision{}, fmt.Errorf("%w: unknown importance %q", ErrInvalid, importance)
	}

	decision := domain.Decision{
		ID:         domain.NewDecisionID(),
		ProjectID:  projectID,
		Title:      title,
		Content:    strings.TrimSpace(content),
		Importance: importance,
		Status:     domain.DecisionActive,
		Supersedes: supersedes,
	}

	var superseded *domain.Decision
	err := s.store.WithTx(ctx, func(tx *postgres.Store) error {
		if supersedes != nil {
			previous, err := tx.DecisionByID(ctx, *supersedes)
			if err != nil {
				return fmt.Errorf("%w: unknown decision %s", ErrInvalid, *supersedes)
			}
			if previous.ProjectID != projectID {
				return fmt.Errorf("%w: decision %s belongs to another project", ErrInvalid, *supersedes)
			}
			if err := tx.CreateDecision(ctx, &decision); err != nil {
				return err
			}
			replaced, err := tx.SupersedeDecision(ctx, *supersedes)
			if err != nil {
				if errors.Is(err, postgres.ErrNotFound) {
					return fmt.Errorf("%w: decision %s is already superseded", ErrConflict, *supersedes)
				}
				return err
			}
			superseded = &replaced
			return nil
		}
		return tx.CreateDecision(ctx, &decision)
	})
	if err != nil {
		return domain.Decision{}, translateKnowledge(err)
	}

	scope := domain.Scope{ProjectID: projectID}
	s.emit(ctx, identity.UserID, events.TypeDecisionCreated, scope, DecisionPayload{
		DecisionID: string(decision.ID), Title: decision.Title,
		Importance: decision.Importance.String(),
	})
	if superseded != nil {
		s.emit(ctx, identity.UserID, events.TypeDecisionSuperseded, scope, DecisionPayload{
			DecisionID: string(superseded.ID), Title: superseded.Title,
			Importance:   superseded.Importance.String(),
			SupersededBy: string(decision.ID),
		})
	}
	return decision, nil
}

// ListDecisions returns the Decisions of a Project.
// DeleteDecision removes a Decision from the Project memory.
//
// Whatever it had superseded becomes current again: a Decision is marked
// superseded because something replaced it, and if that something is gone the
// Project would otherwise be left with a record it no longer reads and nothing
// in its place. Superseding remains the honest move when a decision was
// changed; deleting is for one that should never have been recorded.
func (s *Service) DeleteDecision(ctx context.Context, identity auth.Identity, id domain.DecisionID) error {
	decision, err := s.store.GetDecision(ctx, identity.UserID, id)
	if err != nil {
		return translate(err)
	}

	err = s.store.WithTx(ctx, func(tx *postgres.Store) error {
		if decision.Supersedes != nil {
			if err := tx.ReviveDecision(ctx, *decision.Supersedes); err != nil {
				return err
			}
		}
		return tx.DeleteDecision(ctx, identity.UserID, id)
	})
	if err != nil {
		return translate(err)
	}

	s.emit(ctx, identity.UserID, events.TypeDecisionDeleted,
		domain.Scope{ProjectID: decision.ProjectID}, DecisionPayload{
			DecisionID: string(decision.ID), Title: decision.Title,
			Importance: decision.Importance.String(),
		})
	return nil
}

func (s *Service) ListDecisions(ctx context.Context, identity auth.Identity, projectID domain.ProjectID, includeSuperseded bool) ([]domain.Decision, error) {
	decisions, err := s.store.ListDecisions(ctx, identity.UserID, projectID, includeSuperseded)
	return decisions, translate(err)
}

// SearchDecisions finds Decisions by text. NORMAL decisions are not injected
// into any context, so this is how they are found.
func (s *Service) SearchDecisions(ctx context.Context, identity auth.Identity, projectID domain.ProjectID, query string, limit int) ([]domain.Decision, error) {
	if _, err := s.store.GetProject(ctx, identity.UserID, projectID); err != nil {
		return nil, translate(err)
	}
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("%w: a search needs a query", ErrInvalid)
	}
	decisions, err := s.store.SearchDecisions(ctx, projectID, query, searchLimit(limit))
	return decisions, translate(err)
}

// SearchHistory searches the episodic layer of project memory: what was said and
// done in earlier Sessions.
func (s *Service) SearchHistory(ctx context.Context, identity auth.Identity, projectID domain.ProjectID, query string, limit int) ([]postgres.HistoryHit, error) {
	if _, err := s.store.GetProject(ctx, identity.UserID, projectID); err != nil {
		return nil, translate(err)
	}
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("%w: a search needs a query", ErrInvalid)
	}
	hits, err := s.store.SearchHistory(ctx, projectID, query, searchLimit(limit))
	return hits, translate(err)
}

// searchLimit bounds a result set to something an agent can read.
func searchLimit(limit int) int {
	if limit <= 0 {
		return defaultSearchLimit
	}
	if limit > maxSearchLimit {
		return maxSearchLimit
	}
	return limit
}

// translateKnowledge adds the knowledge-specific storage errors to the usual
// mapping.
func translateKnowledge(err error) error {
	if errors.Is(err, postgres.ErrDependencyCycle) {
		return fmt.Errorf("%w: that dependency would create a cycle", ErrInvalid)
	}
	if errors.Is(err, ErrInvalid) || errors.Is(err, ErrConflict) {
		return err
	}
	return translate(err)
}
