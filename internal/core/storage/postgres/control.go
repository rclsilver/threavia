package postgres

import (
	"context"

	"github.com/rclsilver/threavia/internal/core/domain"
)

const controlColumns = `id,owner_id,label,scopes,created_at,expires_at,revoked_at,bootstrap`

func scanControl(row scanner) (domain.ControlAccess, error) {
	var a domain.ControlAccess
	err := row.Scan(&a.ID, &a.OwnerID, &a.Label, &a.Scopes, &a.CreatedAt, &a.ExpiresAt, &a.RevokedAt, &a.Bootstrap)
	return a, classify(err, "read MCP control access")
}

func (s *Store) CreateControlAccess(ctx context.Context, a domain.ControlAccess, hash string) (domain.ControlAccess, error) {
	return scanControl(s.q.QueryRow(ctx, `INSERT INTO control_accesses(id,owner_id,label,scopes,secret_hash,expires_at,bootstrap) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING `+controlColumns, a.ID, a.OwnerID, a.Label, a.Scopes, hash, a.ExpiresAt, a.Bootstrap))
}

// Bootstrap is insert-only, including after revocation. Environment values can
// never resurrect an access or widen an existing grant on process restart.
func (s *Store) BootstrapControlAccess(ctx context.Context, a domain.ControlAccess, hash string) error {
	_, err := s.q.Exec(ctx, `INSERT INTO control_accesses(id,owner_id,label,scopes,secret_hash,bootstrap) VALUES($1,$2,$3,$4,$5,true) ON CONFLICT DO NOTHING`, a.ID, a.OwnerID, a.Label, a.Scopes, hash)
	return classify(err, "bootstrap MCP control access")
}

func (s *Store) ControlAccessByHash(ctx context.Context, hash string) (domain.ControlAccess, error) {
	return scanControl(s.q.QueryRow(ctx, `SELECT `+controlColumns+` FROM control_accesses WHERE secret_hash=$1 AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at>clock_timestamp())`, hash))
}
func (s *Store) ControlAccess(ctx context.Context, owner domain.UserID, id string) (domain.ControlAccess, error) {
	return scanControl(s.q.QueryRow(ctx, `SELECT `+controlColumns+` FROM control_accesses WHERE id=$1 AND owner_id=$2 AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at>clock_timestamp())`, id, owner))
}
func (s *Store) ListControlAccesses(ctx context.Context, owner domain.UserID) ([]domain.ControlAccess, error) {
	rows, err := s.q.Query(ctx, `SELECT `+controlColumns+` FROM control_accesses WHERE owner_id=$1 ORDER BY created_at DESC`, owner)
	if err != nil {
		return nil, classify(err, "list MCP control accesses")
	}
	defer rows.Close()
	out := []domain.ControlAccess{}
	for rows.Next() {
		a, err := scanControl(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
func (s *Store) RevokeControlAccess(ctx context.Context, owner domain.UserID, id string) error {
	tag, err := s.q.Exec(ctx, `UPDATE control_accesses SET revoked_at=COALESCE(revoked_at,clock_timestamp()) WHERE id=$1 AND owner_id=$2`, id, owner)
	if err != nil {
		return classify(err, "revoke MCP control access")
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
