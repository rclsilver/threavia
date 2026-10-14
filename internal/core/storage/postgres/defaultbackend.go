package postgres

import (
	"context"

	"github.com/rclsilver/threavia/internal/core/domain"
)

// Serialize choices by owner, including clears, so concurrent clients cannot
// leave two defaults or erase a freshly saved choice during validation.
func (s *Store) SetDefaultBackend(ctx context.Context, ownerID domain.UserID, id domain.BackendInstanceID) error {
	return s.WithTx(ctx, func(tx *Store) error {
		if _, err := tx.q.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "default-backend:"+string(ownerID)); err != nil {
			return classify(err, "lock default backend")
		}
		if id != "" {
			var status domain.BackendOwnershipStatus
			if err := tx.q.QueryRow(ctx, `SELECT ownership_status FROM backend_instances WHERE id = $1 AND owner_id = $2 FOR UPDATE`, id, ownerID).Scan(&status); err != nil {
				return classify(err, "read default backend")
			}
			if status != domain.BackendClaimed {
				return ErrConflict
			}
		}
		if _, err := tx.q.Exec(ctx, `UPDATE backend_instances SET is_default = FALSE WHERE owner_id = $1 AND is_default`, ownerID); err != nil {
			return classify(err, "clear default backend")
		}
		if id == "" {
			return nil
		}
		_, err := tx.q.Exec(ctx, `UPDATE backend_instances SET is_default = TRUE WHERE id = $1 AND owner_id = $2`, id, ownerID)
		return classify(err, "set default backend")
	})
}
