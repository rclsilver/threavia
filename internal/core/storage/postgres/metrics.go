package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// MetricGroup contains no execution identifiers or filesystem paths.
type MetricGroup struct {
	ProjectID, BackendInstanceID, KnownDirectoryID, State string
	Count, Seconds                                        float64
}

type MetricName struct{ Kind, ID, Name string }

// MetricsSnapshot is a database-wide snapshot, shared by all Core replicas.
type MetricsSnapshot struct {
	Active, Finished, Durations []MetricGroup
	Names                       []MetricName
}

// ReadMetrics bounds reads to active jobs and compact durable aggregates. A
// repeatable-read transaction prevents a scrape from mixing different states.
func (s *Store) ReadMetrics(ctx context.Context) (MetricsSnapshot, error) {
	var snapshot MetricsSnapshot
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return snapshot, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := []struct {
		sql    string
		target *[]MetricGroup
	}{
		{`SELECT s.project_id::text, r.backend_instance_id::text,
			COALESCE((CASE WHEN NOT j.metrics_directory_frozen THEN s.working_directory_id ELSE j.metrics_directory_id END)::text, ''),
            j.status, count(*)::float8,
            GREATEST(0, EXTRACT(EPOCH FROM clock_timestamp() - min(j.state_entered_at)))::float8
          FROM jobs j JOIN runs r ON r.id = j.run_id JOIN sessions s ON s.id = r.session_id
          WHERE j.status NOT IN ('COMPLETED','FAILED','CANCELLED') GROUP BY 1,2,3,4`, &snapshot.Active},
		{`SELECT project_id, backend_instance_id, known_directory_id, outcome, total::float8, 0::float8 FROM job_metric_totals`, &snapshot.Finished},
		{`SELECT project_id, backend_instance_id, known_directory_id, state, exits::float8, seconds FROM job_state_metric_totals`, &snapshot.Durations},
	}
	for _, query := range queries {
		rows, err := tx.Query(ctx, query.sql)
		if err != nil {
			return snapshot, err
		}
		for rows.Next() {
			var g MetricGroup
			if err := rows.Scan(&g.ProjectID, &g.BackendInstanceID, &g.KnownDirectoryID, &g.State, &g.Count, &g.Seconds); err != nil {
				rows.Close()
				return snapshot, err
			}
			*query.target = append(*query.target, g)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return snapshot, err
		}
	}
	rows, err := tx.Query(ctx, `SELECT 'project', id::text, name FROM projects
        UNION ALL SELECT 'backend', id::text, name FROM backend_instances
        UNION ALL SELECT 'known_directory', id::text, name FROM known_directories`)
	if err != nil {
		return snapshot, err
	}
	for rows.Next() {
		var n MetricName
		if err := rows.Scan(&n.Kind, &n.ID, &n.Name); err != nil {
			rows.Close()
			return snapshot, err
		}
		snapshot.Names = append(snapshot.Names, n)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return snapshot, err
	}
	return snapshot, tx.Commit(ctx)
}
