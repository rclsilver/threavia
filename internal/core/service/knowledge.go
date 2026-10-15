package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
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

	b := &batch{ownerID: identity.UserID}
	err := s.store.WithTx(ctx, func(tx *postgres.Store) error {
		if _, err := tx.LockProject(ctx, identity.UserID, projectID); err != nil {
			return err
		}
		if err := tx.CreateTask(ctx, &task); err != nil {
			return err
		}
		for _, dependency := range dependsOn {
			if slices.Contains(task.DependsOn, dependency) {
				continue
			}
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
		return s.recordAuditedChange(ctx, tx, b, identity, events.TypeTaskCreated, domain.Scope{ProjectID: projectID}, string(task.ID), taskAuditPayload(task))
	})
	if err != nil {
		return domain.Task{}, translateKnowledge(err)
	}

	s.flush(b)
	return task, nil
}

// UpdateTask changes a Task. Empty fields are left alone, so a status change
// does not have to resend the prose.
func (s *Service) UpdateTask(ctx context.Context, identity auth.Identity, id domain.TaskID, title, description string, status domain.TaskStatus) (domain.Task, error) {
	return s.mutateTask(ctx, identity, id, events.TypeTaskUpdated, func(tx *postgres.Store, current *domain.Task, _ *TaskPayload) error {
		if status != "" {
			if !status.Valid() {
				return fmt.Errorf("%w: unknown task status %q", ErrInvalid, status)
			}
			if status != current.Status && !current.Status.CanTransition(status) {
				return fmt.Errorf("%w: a task cannot go from %s to %s", ErrConflict, current.Status, status)
			}
		}
		var err error
		*current, err = tx.UpdateTask(ctx, id, strings.TrimSpace(title), strings.TrimSpace(description), status)
		return err
	})
}

// DeleteTask removes a Task for good.
//
// Filing one is cheap and an agent files them freely, so taking one back has to
// be cheap too, or a Project's list of what is left to do fills with work
// nobody ever meant to do. What was done about it stays in the timeline, which
// is where history lives; this only drops the entry from the list.
func (s *Service) DeleteTask(ctx context.Context, identity auth.Identity, id domain.TaskID) error {
	_, err := s.mutateTask(ctx, identity, id, events.TypeTaskDeleted, func(tx *postgres.Store, _ *domain.Task, _ *TaskPayload) error {
		return tx.DeleteTask(ctx, identity.UserID, id)
	})
	return err
}

// AddTaskDependency records that a Task waits on another.
//
// The two must belong to the same Project, and the edge must not close a loop:
// nothing on a cycle is ever ready, so every Task on it would wait for good.
func (s *Service) AddTaskDependency(ctx context.Context, identity auth.Identity, id, dependsOn domain.TaskID) (domain.Task, error) {
	return s.mutateTask(ctx, identity, id, events.TypeTaskUpdated, func(tx *postgres.Store, task *domain.Task, payload *TaskPayload) error {
		blocker, err := tx.GetTask(ctx, identity.UserID, dependsOn)
		if err != nil {
			return fmt.Errorf("%w: unknown dependency %s", ErrInvalid, dependsOn)
		}
		if blocker.ProjectID != task.ProjectID {
			return fmt.Errorf("%w: dependency %s belongs to another project", ErrInvalid, dependsOn)
		}
		if err := tx.AddTaskDependency(ctx, id, dependsOn); err != nil {
			return err
		}
		payload.DependencyAction, payload.DependencyTaskID, payload.DependencyTitle = "added", dependsOn, blocker.Title
		*task, err = tx.GetTask(ctx, identity.UserID, id)
		return err
	})
}

// RemoveTaskDependency stops a Task from waiting on another.
func (s *Service) RemoveTaskDependency(ctx context.Context, identity auth.Identity, id, dependsOn domain.TaskID) (domain.Task, error) {
	return s.mutateTask(ctx, identity, id, events.TypeTaskUpdated, func(tx *postgres.Store, task *domain.Task, payload *TaskPayload) error {
		if !slices.Contains(task.DependsOn, dependsOn) {
			return nil
		}
		blocker, err := tx.GetTask(ctx, identity.UserID, dependsOn)
		if err != nil {
			return err
		}
		if err := tx.RemoveTaskDependency(ctx, id, dependsOn); err != nil {
			return err
		}
		payload.DependencyAction, payload.DependencyTaskID, payload.DependencyTitle = "removed", dependsOn, blocker.Title
		*task, err = tx.GetTask(ctx, identity.UserID, id)
		return err
	})
}

// A project lock makes graph checks and audit before/after values consistent,
// including when two dependency additions would otherwise form a cycle.
func (s *Service) mutateTask(ctx context.Context, identity auth.Identity, id domain.TaskID, kind events.Type, change func(*postgres.Store, *domain.Task, *TaskPayload) error) (domain.Task, error) {
	task, err := s.store.GetTask(ctx, identity.UserID, id)
	if err != nil {
		return domain.Task{}, translate(err)
	}
	b := &batch{ownerID: identity.UserID}
	err = s.store.WithTx(ctx, func(tx *postgres.Store) error {
		if _, err := tx.LockProject(ctx, identity.UserID, task.ProjectID); err != nil {
			return err
		}
		task, err = tx.GetTask(ctx, identity.UserID, id)
		if err != nil {
			return err
		}
		before, extra := task, TaskPayload{}
		if err := change(tx, &task, &extra); err != nil {
			return err
		}
		payload := taskAuditPayload(task)
		payload.DependencyAction, payload.DependencyTaskID, payload.DependencyTitle = extra.DependencyAction, extra.DependencyTaskID, extra.DependencyTitle
		if kind == events.TypeTaskUpdated {
			if task.Title != before.Title {
				payload.ChangedFields = append(payload.ChangedFields, "title")
			}
			if task.Description != before.Description {
				payload.ChangedFields = append(payload.ChangedFields, "description")
			}
			if task.Status != before.Status {
				payload.ChangedFields = append(payload.ChangedFields, "status")
				payload.PreviousStatus = before.Status.String()
			}
			if !slices.Equal(task.DependsOn, before.DependsOn) {
				payload.ChangedFields = append(payload.ChangedFields, "dependsOn")
			}
			if len(payload.ChangedFields) == 0 {
				return nil
			}
		}
		return s.recordAuditedChange(ctx, tx, b, identity, kind, domain.Scope{ProjectID: task.ProjectID}, string(id), payload)
	})
	if err != nil {
		return domain.Task{}, translateKnowledge(err)
	}
	s.flush(b)
	return task, nil
}

func taskAuditPayload(task domain.Task) TaskPayload {
	return TaskPayload{TaskID: string(task.ID), Title: task.Title, Status: task.Status.String(), DependsOn: task.DependsOn}
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
// createdBy is the Job whose agent recorded it, and nil when a person did.
func (s *Service) CreateDecision(ctx context.Context, identity auth.Identity, projectID domain.ProjectID, title, content string, importance domain.DecisionImportance, supersedes *domain.DecisionID, createdBy *domain.JobID) (domain.Decision, error) {
	if projectID != "" {
		if _, err := s.store.GetProject(ctx, identity.UserID, projectID); err != nil {
			return domain.Decision{}, translate(err)
		}
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
		ID:             domain.NewDecisionID(),
		ProjectID:      projectID,
		OwnerID:        identity.UserID,
		Title:          title,
		Content:        strings.TrimSpace(content),
		Importance:     importance,
		Status:         domain.DecisionActive,
		Supersedes:     supersedes,
		CreatedByJobID: createdBy,
	}

	var superseded *domain.Decision
	b := &batch{ownerID: identity.UserID}
	err := s.store.WithTx(ctx, func(tx *postgres.Store) error {
		if err := tx.LockDecisionMemory(ctx, identity.UserID); err != nil {
			return err
		}
		if projectID != "" {
			if _, err := tx.LockProject(ctx, identity.UserID, projectID); err != nil {
				return err
			}
		}
		if supersedes != nil {
			previous, err := tx.DecisionByID(ctx, *supersedes)
			if err != nil {
				return fmt.Errorf("%w: unknown decision %s", ErrInvalid, *supersedes)
			}
			if previous.OwnerID != identity.UserID || (projectID != "" && previous.ProjectID != projectID) {
				return fmt.Errorf("%w: decision %s belongs to another memory scope", ErrInvalid, *supersedes)
			}
			if projectID == "" && previous.ProjectID != "" {
				if _, err := tx.LockProject(ctx, identity.UserID, previous.ProjectID); err != nil {
					return err
				}
			}
			// A person can promote any owned project fact. An agent can only
			// promote a fact from the project of its calling Job.
			if projectID == "" && previous.ProjectID != "" && createdBy != nil {
				jc, err := tx.LoadJobContext(ctx, *createdBy)
				if err != nil {
					return err
				}
				if jc.OwnerID != identity.UserID || jc.ProjectID != previous.ProjectID {
					return fmt.Errorf("%w: decision %s belongs to another project", ErrInvalid, *supersedes)
				}
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
		} else if err := tx.CreateDecision(ctx, &decision); err != nil {
			return err
		}
		payload := decisionAuditPayload(decision)
		if superseded != nil {
			payload.Supersedes = string(superseded.ID)
			if superseded.ProjectID != projectID {
				payload.PreviousProjectID = superseded.ProjectID
			}
		}
		if err := s.recordAuditedChange(ctx, tx, b, identity, events.TypeDecisionCreated, domain.Scope{ProjectID: projectID}, string(decision.ID), payload); err != nil {
			return err
		}
		if superseded != nil {
			previous := decisionAuditPayload(*superseded)
			previous.SupersededBy = string(decision.ID)
			if superseded.ProjectID != projectID {
				previous.PreviousProjectID = superseded.ProjectID
			}
			return s.recordAuditedChange(ctx, tx, b, identity, events.TypeDecisionSuperseded, domain.Scope{ProjectID: superseded.ProjectID}, string(superseded.ID), previous)
		}
		return nil
	})
	if err != nil {
		return domain.Decision{}, translateKnowledge(err)
	}

	s.flush(b)
	return decision, nil
}

// SetDecisionImportance pins a Decision to every Job of its Project, or
// unpins it. Only the importance changes: what was decided is never edited,
// a decision that changed is superseded.
func (s *Service) SetDecisionImportance(ctx context.Context, identity auth.Identity, id domain.DecisionID, importance domain.DecisionImportance) (domain.Decision, error) {
	if !importance.Valid() {
		return domain.Decision{}, fmt.Errorf("%w: unknown importance %q", ErrInvalid, importance)
	}
	var decision domain.Decision
	b := &batch{ownerID: identity.UserID}
	err := s.store.WithTx(ctx, func(tx *postgres.Store) error {
		if err := tx.LockDecisionMemory(ctx, identity.UserID); err != nil {
			return err
		}
		current, err := tx.GetDecision(ctx, identity.UserID, id)
		if err != nil {
			return err
		}
		if current.ProjectID != "" {
			if _, err := tx.LockProject(ctx, identity.UserID, current.ProjectID); err != nil {
				return err
			}
		}
		if current.Importance == importance {
			decision = current
			return nil
		}
		decision, err = tx.SetDecisionImportance(ctx, identity.UserID, id, importance)
		if err != nil {
			return err
		}
		payload := decisionAuditPayload(decision)
		payload.PreviousImportance = current.Importance.String()
		return s.recordAuditedChange(ctx, tx, b, identity, events.TypeDecisionUpdated, domain.Scope{ProjectID: decision.ProjectID}, string(id), payload)
	})
	if err != nil {
		return domain.Decision{}, translate(err)
	}
	s.flush(b)
	return decision, nil
}

// ListDecisions returns the Decisions of a Project.
// DeleteDecision removes a Decision from the Project memory.
//
// Within the same scope, whatever it had superseded becomes current again: a Decision is marked
// superseded because something replaced it, and if that something is gone the
// Project would otherwise be left with a record it no longer reads and nothing
// in its place. Superseding remains the honest move when a decision was
// changed; deleting is for one that should never have been recorded. Deleting
// a promoted global fact leaves its project predecessor historical, so a
// removed fact cannot silently return to the project's active memory.
func (s *Service) DeleteDecision(ctx context.Context, identity auth.Identity, id domain.DecisionID) error {
	b := &batch{ownerID: identity.UserID}
	err := s.store.WithTx(ctx, func(tx *postgres.Store) error {
		if err := tx.LockDecisionMemory(ctx, identity.UserID); err != nil {
			return err
		}
		decision, err := tx.GetDecision(ctx, identity.UserID, id)
		if err != nil {
			return err
		}
		if decision.ProjectID != "" {
			if _, err := tx.LockProject(ctx, identity.UserID, decision.ProjectID); err != nil {
				return err
			}
		}
		var revived *domain.Decision
		if decision.Supersedes != nil {
			previous, err := tx.GetDecision(ctx, identity.UserID, *decision.Supersedes)
			if err != nil && !errors.Is(err, postgres.ErrNotFound) {
				return err
			}
			if err == nil && previous.ProjectID == decision.ProjectID && previous.Status == domain.DecisionSuperseded {
				if err := tx.ReviveDecision(ctx, previous.ID); err != nil {
					return err
				}
				previous.Status = domain.DecisionActive
				revived = &previous
			}
		}
		if err := tx.DeleteDecision(ctx, identity.UserID, id); err != nil {
			return err
		}
		if err := s.recordAuditedChange(ctx, tx, b, identity, events.TypeDecisionDeleted, domain.Scope{ProjectID: decision.ProjectID}, string(id), decisionAuditPayload(decision)); err != nil {
			return err
		}
		if revived != nil {
			payload := decisionAuditPayload(*revived)
			payload.Restored = true
			return s.recordAuditedChange(ctx, tx, b, identity, events.TypeDecisionUpdated, domain.Scope{ProjectID: revived.ProjectID}, string(revived.ID), payload)
		}
		return nil
	})
	if err != nil {
		return translate(err)
	}

	s.flush(b)
	return nil
}

func decisionAuditPayload(decision domain.Decision) DecisionPayload {
	return DecisionPayload{DecisionID: string(decision.ID), Title: decision.Title, Importance: decision.Importance.String(), Status: decision.Status.String()}
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

// SearchMemory reads active global facts plus this Project's facts, or only
// global facts with an empty Project. Session history is never broadened.
func (s *Service) SearchMemory(ctx context.Context, identity auth.Identity, projectID domain.ProjectID, query string, limit int) ([]domain.Decision, error) {
	if projectID != "" {
		if _, err := s.store.GetProject(ctx, identity.UserID, projectID); err != nil {
			return nil, translate(err)
		}
	}
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("%w: a search needs a query", ErrInvalid)
	}
	decisions, err := s.store.SearchMemory(ctx, identity.UserID, projectID, query, searchLimit(limit))
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
