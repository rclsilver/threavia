package postgres

import (
	"context"

	"github.com/rclsilver/threavia/internal/core/domain"
)

const decisionColumns = `d.id, d.project_id, d.title, d.content, d.importance,
	d.status, d.supersedes, d.created_at, d.updated_at`

// CreateDecision inserts a Decision.
func (s *Store) CreateDecision(ctx context.Context, decision *domain.Decision) error {
	err := s.q.QueryRow(ctx, `
		INSERT INTO decisions (id, project_id, title, content, importance, status, supersedes)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING created_at, updated_at`,
		decision.ID, decision.ProjectID, decision.Title, decision.Content,
		decision.Importance, decision.Status, decision.Supersedes,
	).Scan(&decision.CreatedAt, &decision.UpdatedAt)
	return classify(err, "create decision")
}

// SupersedeDecision marks an older Decision replaced. It remains historical and
// stops being injected into Run contexts.
func (s *Store) SupersedeDecision(ctx context.Context, id domain.DecisionID) (domain.Decision, error) {
	return scanDecision(s.q.QueryRow(ctx, `
		UPDATE decisions d SET status = 'SUPERSEDED', updated_at = now()
		WHERE d.id = $1 AND d.status = 'ACTIVE'
		RETURNING `+decisionColumns, id))
}

// GetDecision returns a Decision the user can access through its Project.
func (s *Store) GetDecision(ctx context.Context, ownerID domain.UserID, id domain.DecisionID) (domain.Decision, error) {
	return scanDecision(s.q.QueryRow(ctx, `
		SELECT `+decisionColumns+`
		FROM decisions d JOIN projects p ON p.id = d.project_id
		WHERE d.id = $1 AND p.owner_id = $2`, id, ownerID))
}

// DecisionByID returns a Decision without an ownership check, for the
// backend-facing paths where the Project is already resolved.
func (s *Store) DecisionByID(ctx context.Context, id domain.DecisionID) (domain.Decision, error) {
	return scanDecision(s.q.QueryRow(ctx,
		`SELECT `+decisionColumns+` FROM decisions d WHERE d.id = $1`, id))
}

// ListDecisions returns the Decisions of a Project, most recent first.
func (s *Store) ListDecisions(ctx context.Context, ownerID domain.UserID, projectID domain.ProjectID, includeSuperseded bool) ([]domain.Decision, error) {
	rows, err := s.q.Query(ctx, `
		SELECT `+decisionColumns+`
		FROM decisions d JOIN projects p ON p.id = d.project_id
		WHERE d.project_id = $1 AND p.owner_id = $2 AND ($3 OR d.status = 'ACTIVE')
		ORDER BY d.created_at DESC`, projectID, ownerID, includeSuperseded)
	if err != nil {
		return nil, classify(err, "list decisions")
	}
	defer rows.Close()

	return collect(rows, scanDecision, "list decisions")
}

// ImportantDecisions returns the active IMPORTANT Decisions of a Project, which
// are the ones carried into every new Run context (spec section 12).
func (s *Store) ImportantDecisions(ctx context.Context, projectID domain.ProjectID, limit int) ([]domain.Decision, error) {
	rows, err := s.q.Query(ctx, `
		SELECT `+decisionColumns+`
		FROM decisions d
		WHERE d.project_id = $1 AND d.status = 'ACTIVE' AND d.importance = 'IMPORTANT'
		ORDER BY d.created_at DESC
		LIMIT $2`, projectID, limit)
	if err != nil {
		return nil, classify(err, "list important decisions")
	}
	defer rows.Close()

	return collect(rows, scanDecision, "list important decisions")
}

// SearchDecisions runs a full-text search over a Project's Decisions. NORMAL
// decisions are not injected anywhere, so this is how an agent finds them.
func (s *Store) SearchDecisions(ctx context.Context, projectID domain.ProjectID, query string, limit int) ([]domain.Decision, error) {
	rows, err := s.q.Query(ctx, `
		SELECT `+decisionColumns+`
		FROM decisions d
		WHERE d.project_id = $1 AND d.search @@ websearch_to_tsquery('simple', $2)
		ORDER BY ts_rank(d.search, websearch_to_tsquery('simple', $2)) DESC, d.created_at DESC
		LIMIT $3`, projectID, query, limit)
	if err != nil {
		return nil, classify(err, "search decisions")
	}
	defer rows.Close()

	return collect(rows, scanDecision, "search decisions")
}

func scanDecision(row scanner) (domain.Decision, error) {
	var d domain.Decision
	err := row.Scan(&d.ID, &d.ProjectID, &d.Title, &d.Content, &d.Importance,
		&d.Status, &d.Supersedes, &d.CreatedAt, &d.UpdatedAt)
	return d, classify(err, "read decision")
}
