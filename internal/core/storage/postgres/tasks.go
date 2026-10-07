package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/rclsilver/threavia/internal/core/domain"
)

// ErrDependencyCycle is returned when an edge would close a loop in the task
// graph, which section 14 forbids.
var ErrDependencyCycle = errors.New("the dependency would create a cycle")

const taskColumns = `t.id, t.project_id, t.title, t.description, t.status,
	t.created_at, t.updated_at, t.completed_at`

// CreateTask inserts a Task.
func (s *Store) CreateTask(ctx context.Context, task *domain.Task) error {
	err := s.q.QueryRow(ctx, `
		INSERT INTO tasks (id, project_id, title, description, status)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING created_at, updated_at`,
		task.ID, task.ProjectID, task.Title, task.Description, task.Status,
	).Scan(&task.CreatedAt, &task.UpdatedAt)
	return classify(err, "create task")
}

// GetTask returns a Task the user can access through its Project, with its
// dependencies.
func (s *Store) GetTask(ctx context.Context, ownerID domain.UserID, id domain.TaskID) (domain.Task, error) {
	task, err := scanTask(s.q.QueryRow(ctx, `
		SELECT `+taskColumns+`
		FROM tasks t JOIN projects p ON p.id = t.project_id
		WHERE t.id = $1 AND p.owner_id = $2`, id, ownerID))
	if err != nil {
		return task, err
	}
	return s.withDependencies(ctx, task)
}

// TaskByID returns a Task without an ownership check, for the backend-facing
// paths where the Project is already resolved.
func (s *Store) TaskByID(ctx context.Context, id domain.TaskID) (domain.Task, error) {
	task, err := scanTask(s.q.QueryRow(ctx,
		`SELECT `+taskColumns+` FROM tasks t WHERE t.id = $1`, id))
	if err != nil {
		return task, err
	}
	return s.withDependencies(ctx, task)
}

// UpdateTask changes the mutable fields of a Task. Empty strings leave the
// corresponding field alone, so a caller can touch the status without resending
// the prose.
func (s *Store) UpdateTask(ctx context.Context, id domain.TaskID, title, description string, status domain.TaskStatus) (domain.Task, error) {
	var completedAt *time.Time
	if status == domain.TaskDone {
		now := time.Now().UTC()
		completedAt = &now
	}

	task, err := scanTask(s.q.QueryRow(ctx, `
		UPDATE tasks t
		SET title = COALESCE(NULLIF($2, ''), t.title),
		    description = COALESCE(NULLIF($3, ''), t.description),
		    status = COALESCE(NULLIF($4, ''), t.status),
		    completed_at = CASE
		        WHEN NULLIF($4, '') = 'DONE' THEN COALESCE(t.completed_at, $5)
		        WHEN NULLIF($4, '') IS NULL THEN t.completed_at
		        ELSE NULL
		    END,
		    updated_at = now()
		WHERE t.id = $1
		RETURNING `+taskColumns, id, title, description, string(status), completedAt))
	if err != nil {
		return task, err
	}
	return s.withDependencies(ctx, task)
}

// ListTasks returns the Tasks of a Project, oldest first.
func (s *Store) ListTasks(ctx context.Context, ownerID domain.UserID, projectID domain.ProjectID, includeDone bool) ([]domain.Task, error) {
	rows, err := s.q.Query(ctx, `
		SELECT `+taskColumns+`
		FROM tasks t JOIN projects p ON p.id = t.project_id
		WHERE t.project_id = $1 AND p.owner_id = $2 AND ($3 OR t.status <> 'DONE')
		ORDER BY t.created_at`, projectID, ownerID, includeDone)
	if err != nil {
		return nil, classify(err, "list tasks")
	}
	defer rows.Close()

	tasks, err := collect(rows, scanTask, "list tasks")
	if err != nil {
		return nil, err
	}
	return s.withAllDependencies(ctx, tasks)
}

// ReadyTasks returns the TODO Tasks whose dependencies are all DONE. Blocked is
// derived here rather than stored, so it cannot go stale.
func (s *Store) ReadyTasks(ctx context.Context, projectID domain.ProjectID, limit int) ([]domain.Task, error) {
	rows, err := s.q.Query(ctx, `
		SELECT `+taskColumns+`
		FROM tasks t
		WHERE t.project_id = $1 AND t.status = 'TODO'
		  AND NOT EXISTS (
		      SELECT 1
		      FROM task_dependencies d
		      JOIN tasks blocker ON blocker.id = d.depends_on_task_id
		      WHERE d.task_id = t.id AND blocker.status <> 'DONE'
		  )
		ORDER BY t.created_at
		LIMIT $2`, projectID, limit)
	if err != nil {
		return nil, classify(err, "list ready tasks")
	}
	defer rows.Close()

	return collect(rows, scanTask, "list ready tasks")
}

// OpenTasks returns the Tasks worth putting in a Run context: what is in
// progress, then what is ready to start.
func (s *Store) OpenTasks(ctx context.Context, projectID domain.ProjectID, limit int) ([]domain.Task, error) {
	rows, err := s.q.Query(ctx, `
		SELECT `+taskColumns+`
		FROM tasks t
		WHERE t.project_id = $1
		  AND (t.status = 'IN_PROGRESS' OR (t.status = 'TODO' AND NOT EXISTS (
		      SELECT 1
		      FROM task_dependencies d
		      JOIN tasks blocker ON blocker.id = d.depends_on_task_id
		      WHERE d.task_id = t.id AND blocker.status <> 'DONE'
		  )))
		ORDER BY CASE t.status WHEN 'IN_PROGRESS' THEN 0 ELSE 1 END, t.created_at
		LIMIT $2`, projectID, limit)
	if err != nil {
		return nil, classify(err, "list open tasks")
	}
	defer rows.Close()

	return collect(rows, scanTask, "list open tasks")
}

// SearchTasks runs a full-text search over a Project's Tasks.
func (s *Store) SearchTasks(ctx context.Context, projectID domain.ProjectID, query string, limit int) ([]domain.Task, error) {
	rows, err := s.q.Query(ctx, `
		SELECT `+taskColumns+`
		FROM tasks t
		WHERE t.project_id = $1 AND t.search @@ websearch_to_tsquery('simple', $2)
		ORDER BY ts_rank(t.search, websearch_to_tsquery('simple', $2)) DESC, t.created_at
		LIMIT $3`, projectID, query, limit)
	if err != nil {
		return nil, classify(err, "search tasks")
	}
	defer rows.Close()

	return collect(rows, scanTask, "search tasks")
}

// AddTaskDependency records that one Task waits on another.
//
// The cycle check walks the graph from the prospective dependency: if the
// dependent Task is already reachable from it, the new edge would close a loop.
// It runs in the same statement as the insert would, so two concurrent writers
// cannot each see an acyclic graph and together create a cycle.
func (s *Store) AddTaskDependency(ctx context.Context, taskID, dependsOn domain.TaskID) error {
	if taskID == dependsOn {
		return ErrDependencyCycle
	}

	var cycle bool
	err := s.q.QueryRow(ctx, `
		WITH RECURSIVE reachable(id) AS (
		    SELECT depends_on_task_id FROM task_dependencies WHERE task_id = $2
		    UNION
		    SELECT d.depends_on_task_id
		    FROM task_dependencies d JOIN reachable r ON d.task_id = r.id
		)
		SELECT EXISTS (SELECT 1 FROM reachable WHERE id = $1)`, taskID, dependsOn).Scan(&cycle)
	if err != nil {
		return classify(err, "check for a dependency cycle")
	}
	if cycle {
		return ErrDependencyCycle
	}

	_, err = s.q.Exec(ctx, `
		INSERT INTO task_dependencies (task_id, depends_on_task_id)
		VALUES ($1, $2)
		ON CONFLICT DO NOTHING`, taskID, dependsOn)
	return classify(err, "add a task dependency")
}

// DeleteTask removes a Task for good.
//
// The edges of the graph and the Jobs that worked on it go with it, by the
// cascade the schema declares: an edge to a Task that no longer exists would
// block its dependents on nothing.
func (s *Store) DeleteTask(ctx context.Context, ownerID domain.UserID, id domain.TaskID) error {
	tag, err := s.q.Exec(ctx, `
		DELETE FROM tasks t
		USING projects p
		WHERE t.id = $1 AND t.project_id = p.id AND p.owner_id = $2`, id, ownerID)
	if err != nil {
		return classify(err, "delete a task")
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// RemoveTaskDependency drops one edge of the graph.
//
// Removing what is not there succeeds: the caller asked for a Task that no
// longer waits on another, and that is the state it gets.
func (s *Store) RemoveTaskDependency(ctx context.Context, taskID, dependsOn domain.TaskID) error {
	_, err := s.q.Exec(ctx, `
		DELETE FROM task_dependencies
		WHERE task_id = $1 AND depends_on_task_id = $2`, taskID, dependsOn)
	return classify(err, "remove a task dependency")
}

// LinkJobToTask records that a Job worked on a Task.
func (s *Store) LinkJobToTask(ctx context.Context, jobID domain.JobID, taskID domain.TaskID) error {
	_, err := s.q.Exec(ctx, `
		INSERT INTO job_tasks (job_id, task_id) VALUES ($1, $2)
		ON CONFLICT DO NOTHING`, jobID, taskID)
	return classify(err, "link a job to a task")
}

// withDependencies fills in the Tasks a Task waits on.
func (s *Store) withDependencies(ctx context.Context, task domain.Task) (domain.Task, error) {
	rows, err := s.q.Query(ctx,
		`SELECT depends_on_task_id FROM task_dependencies WHERE task_id = $1 ORDER BY created_at`, task.ID)
	if err != nil {
		return task, classify(err, "read task dependencies")
	}
	defer rows.Close()

	for rows.Next() {
		var id domain.TaskID
		if err := rows.Scan(&id); err != nil {
			return task, classify(err, "read task dependencies")
		}
		task.DependsOn = append(task.DependsOn, id)
	}
	return task, classify(rows.Err(), "read task dependencies")
}

// withAllDependencies fills in the dependencies of a whole list in one query,
// rather than one round trip per Task.
func (s *Store) withAllDependencies(ctx context.Context, tasks []domain.Task) ([]domain.Task, error) {
	if len(tasks) == 0 {
		return tasks, nil
	}

	ids := make([]domain.TaskID, 0, len(tasks))
	for _, task := range tasks {
		ids = append(ids, task.ID)
	}

	rows, err := s.q.Query(ctx, `
		SELECT task_id, depends_on_task_id
		FROM task_dependencies
		WHERE task_id = ANY($1)
		ORDER BY created_at`, ids)
	if err != nil {
		return nil, classify(err, "read task dependencies")
	}
	defer rows.Close()

	edges := make(map[domain.TaskID][]domain.TaskID)
	for rows.Next() {
		var task, dependsOn domain.TaskID
		if err := rows.Scan(&task, &dependsOn); err != nil {
			return nil, classify(err, "read task dependencies")
		}
		edges[task] = append(edges[task], dependsOn)
	}
	if err := classify(rows.Err(), "read task dependencies"); err != nil {
		return nil, err
	}

	for i := range tasks {
		tasks[i].DependsOn = edges[tasks[i].ID]
	}
	return tasks, nil
}

func scanTask(row scanner) (domain.Task, error) {
	var t domain.Task
	err := row.Scan(&t.ID, &t.ProjectID, &t.Title, &t.Description, &t.Status,
		&t.CreatedAt, &t.UpdatedAt, &t.CompletedAt)
	return t, classify(err, "read task")
}
