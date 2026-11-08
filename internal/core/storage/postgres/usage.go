package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/rclsilver/threavia/internal/core/domain"
)

// SessionUsage ranks what the owner's Sessions consumed between two UTC days,
// both included, by tokens of every kind. An empty projectID covers them all.
func (s *Store) SessionUsage(ctx context.Context, ownerID domain.UserID, projectID domain.ProjectID, from, to time.Time, limit int) ([]domain.UsageRow, error) {
	rows, err := s.q.Query(ctx, `
		SELECT u.session_id::text, s.title, p.id::text, p.name, u.backend_instance_id, u.model, u.effort,
		       sum(u.jobs), sum(u.reported_jobs), sum(u.input_tokens), sum(u.output_tokens),
		       sum(u.cache_read_tokens), sum(u.cache_write_tokens), sum(u.cost_usd)
		FROM session_usage_daily u
		JOIN sessions s ON s.id = u.session_id
		JOIN projects p ON p.id = s.project_id
		WHERE p.owner_id = $1 AND ($2 = '' OR p.id::text = $2) AND u.day BETWEEN $3::date AND $4::date
		GROUP BY u.session_id, s.title, p.id, p.name, u.backend_instance_id, u.model, u.effort
		ORDER BY sum(u.input_tokens + u.output_tokens + u.cache_read_tokens + u.cache_write_tokens) DESC,
		         u.session_id, u.model, u.effort
		LIMIT $5`, ownerID, string(projectID), from.Format(time.DateOnly), to.Format(time.DateOnly), limit)
	if err != nil {
		return nil, classify(err, "read session usage")
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.UsageRow, error) {
		var u domain.UsageRow
		err := row.Scan(&u.SessionID, &u.SessionTitle, &u.ProjectID, &u.ProjectName, &u.BackendInstanceID,
			&u.Model, &u.Effort, &u.Jobs, &u.ReportedJobs, &u.InputTokens, &u.OutputTokens,
			&u.CacheReadTokens, &u.CacheWriteTokens, &u.CostUSD)
		return u, err
	})
}
