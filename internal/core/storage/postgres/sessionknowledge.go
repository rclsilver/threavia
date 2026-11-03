package postgres

import (
	"context"

	"github.com/rclsilver/threavia/internal/core/domain"
)

// SessionKnowledge says which tasks and decisions of a Project agents created
// from a Session's Jobs. A decision records its Job; a task does not, so its
// creation is read from the audit trail, through the index on its subject.
func (s *Store) SessionKnowledge(ctx context.Context, owner domain.UserID, id domain.SessionID, project domain.ProjectID) (tasks, decisions []string, err error) {
	read := func(query string) ([]string, error) {
		rows, err := s.q.Query(ctx, query, owner, id, project)
		if err != nil {
			return nil, classify(err, "read session knowledge")
		}
		defer rows.Close()
		ids := []string{}
		for rows.Next() {
			var value string
			if err := rows.Scan(&value); err != nil {
				return nil, classify(err, "read session knowledge")
			}
			ids = append(ids, value)
		}
		return ids, classify(rows.Err(), "read session knowledge")
	}
	if tasks, err = read(`
		SELECT DISTINCT t.id::text FROM tasks t
		JOIN audit_entries a ON a.subject_id = t.id::text
		WHERE t.project_id = $3 AND a.owner_id = $1 AND a.action = 'task.created'
		AND a.job_id IN (SELECT j.id::text FROM jobs j JOIN runs r ON r.id = j.run_id WHERE r.session_id = $2)`); err != nil {
		return nil, nil, err
	}
	decisions, err = read(`
		SELECT d.id::text FROM decisions d
		WHERE d.project_id = $3 AND d.owner_id = $1
		AND d.created_by_job_id IN (SELECT j.id FROM jobs j JOIN runs r ON r.id = j.run_id WHERE r.session_id = $2)`)
	return tasks, decisions, err
}

// Edge is a link between two tasks, or two decisions.
type Edge struct{ From, To string }

// KnowledgeEdges returns the dependency links touching these tasks and the
// supersede links touching these decisions.
func (s *Store) KnowledgeEdges(ctx context.Context, tasks, decisions []string) (taskEdges, decisionEdges []Edge, err error) {
	read := func(query string, ids []string) ([]Edge, error) {
		if len(ids) == 0 {
			return nil, nil
		}
		rows, err := s.q.Query(ctx, query, ids)
		if err != nil {
			return nil, classify(err, "read knowledge links")
		}
		defer rows.Close()
		var edges []Edge
		for rows.Next() {
			var edge Edge
			if err := rows.Scan(&edge.From, &edge.To); err != nil {
				return nil, classify(err, "read knowledge links")
			}
			edges = append(edges, edge)
		}
		return edges, classify(rows.Err(), "read knowledge links")
	}
	if taskEdges, err = read(`SELECT task_id::text, depends_on_task_id::text FROM task_dependencies
		WHERE task_id = ANY($1::uuid[]) OR depends_on_task_id = ANY($1::uuid[])`, tasks); err != nil {
		return nil, nil, err
	}
	decisionEdges, err = read(`SELECT id::text, supersedes::text FROM decisions
		WHERE supersedes IS NOT NULL AND (id = ANY($1::uuid[]) OR supersedes = ANY($1::uuid[]))`, decisions)
	return taskEdges, decisionEdges, err
}

// MoveKnowledge re-files tasks and decisions under another Project.
func (s *Store) MoveKnowledge(ctx context.Context, project domain.ProjectID, tasks, decisions []string) error {
	if len(tasks) > 0 {
		if _, err := s.q.Exec(ctx, `UPDATE tasks SET project_id = $1, updated_at = now() WHERE id = ANY($2::uuid[])`, project, tasks); err != nil {
			return classify(err, "move tasks")
		}
	}
	if len(decisions) > 0 {
		if _, err := s.q.Exec(ctx, `UPDATE decisions SET project_id = $1, updated_at = now() WHERE id = ANY($2::uuid[])`, project, decisions); err != nil {
			return classify(err, "move decisions")
		}
	}
	return nil
}
