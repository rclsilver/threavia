package postgres

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"

	"github.com/rclsilver/threavia/pkg/backend-sdk/quotas"
)

// MetricGroup contains no execution identifiers or filesystem paths.
type MetricGroup struct {
	ProjectID, BackendInstanceID, KnownDirectoryID, State string
	Count, Seconds                                        float64
}

type MetricName struct{ Kind, ID, Name string }

// UsageGroup is what the Jobs of one Session consumed with one model and
// effort, by outcome. ReportedJobs counts those whose backend said anything.
type UsageGroup struct {
	ProjectID, SessionID, BackendInstanceID, Model, Effort, Outcome string
	Jobs, ReportedJobs                                              float64
	InputTokens, OutputTokens, CacheReadTokens, CacheWriteTokens    float64
	CostUSD                                                         float64
}

// WaitGroup counts Jobs waiting for their backend, by reason.
type WaitGroup struct {
	ProjectID, BackendInstanceID, Reason string
	Count                                float64
}

// QuotaSnapshot is the latest quota report of a backend.
type QuotaSnapshot struct {
	BackendInstanceID string
	Status            quotas.Status
}

// MetricsSnapshot is a database-wide snapshot, shared by all Core replicas.
type MetricsSnapshot struct {
	Active, Finished, Durations []MetricGroup
	Usage                       []UsageGroup
	// Waits are wait periods entered since the history began; Waiting are the
	// Jobs waiting now.
	Waits, Waiting []WaitGroup
	Quotas         []QuotaSnapshot
	Names          []MetricName
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
		if err := collectMetrics(ctx, tx, query.sql, query.target, func(rows pgx.Rows, g *MetricGroup) error {
			return rows.Scan(&g.ProjectID, &g.BackendInstanceID, &g.KnownDirectoryID, &g.State, &g.Count, &g.Seconds)
		}); err != nil {
			return snapshot, err
		}
	}
	if err := collectMetrics(ctx, tx, `SELECT project_id, session_id, backend_instance_id, model, effort, outcome,
            jobs::float8, reported_jobs::float8, input_tokens::float8, output_tokens::float8,
            cache_read_tokens::float8, cache_write_tokens::float8, cost_usd
          FROM job_usage_metric_totals`, &snapshot.Usage, func(rows pgx.Rows, g *UsageGroup) error {
		return rows.Scan(&g.ProjectID, &g.SessionID, &g.BackendInstanceID, &g.Model, &g.Effort, &g.Outcome,
			&g.Jobs, &g.ReportedJobs, &g.InputTokens, &g.OutputTokens, &g.CacheReadTokens, &g.CacheWriteTokens, &g.CostUSD)
	}); err != nil {
		return snapshot, err
	}
	scanWait := func(rows pgx.Rows, g *WaitGroup) error {
		return rows.Scan(&g.ProjectID, &g.BackendInstanceID, &g.Reason, &g.Count)
	}
	if err := collectMetrics(ctx, tx, `SELECT project_id, backend_instance_id, reason, total::float8 FROM job_wait_metric_totals`,
		&snapshot.Waits, scanWait); err != nil {
		return snapshot, err
	}
	// A Job parked without a provider report waits for its backend to come
	// back: a disconnection or a lost dispatch.
	if err := collectMetrics(ctx, tx, `SELECT s.project_id::text, r.backend_instance_id::text,
            COALESCE(NULLIF(j.backend_wait->>'reason', ''), 'BACKEND_DISCONNECTED'), count(*)::float8
          FROM jobs j JOIN runs r ON r.id = j.run_id JOIN sessions s ON s.id = r.session_id
          WHERE j.status = 'WAITING_BACKEND' GROUP BY 1,2,3`, &snapshot.Waiting, scanWait); err != nil {
		return snapshot, err
	}
	if err := collectMetrics(ctx, tx, `SELECT id::text, quotas FROM backend_instances WHERE quotas IS NOT NULL`,
		&snapshot.Quotas, func(rows pgx.Rows, q *QuotaSnapshot) error {
			var raw []byte
			if err := rows.Scan(&q.BackendInstanceID, &raw); err != nil {
				return err
			}
			// An unreadable report costs its own gauges, not the whole scrape.
			if json.Unmarshal(raw, &q.Status) != nil {
				q.Status = quotas.Status{}
			}
			return nil
		}); err != nil {
		return snapshot, err
	}
	if err := collectMetrics(ctx, tx, `SELECT 'project', id::text, name FROM projects
        UNION ALL SELECT 'backend', id::text, name FROM backend_instances
        UNION ALL SELECT 'known_directory', id::text, name FROM known_directories
        UNION ALL SELECT 'session', id::text, title FROM sessions`, &snapshot.Names, func(rows pgx.Rows, n *MetricName) error {
		return rows.Scan(&n.Kind, &n.ID, &n.Name)
	}); err != nil {
		return snapshot, err
	}
	return snapshot, tx.Commit(ctx)
}

func collectMetrics[T any](ctx context.Context, tx pgx.Tx, sql string, target *[]T, scan func(pgx.Rows, *T) error) error {
	rows, err := tx.Query(ctx, sql)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var item T
		if err := scan(rows, &item); err != nil {
			return err
		}
		*target = append(*target, item)
	}
	return rows.Err()
}
