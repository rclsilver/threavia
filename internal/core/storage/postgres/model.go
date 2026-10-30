package postgres

import (
	"context"
	"encoding/json"

	"github.com/rclsilver/threavia/internal/core/domain"
)

// LockSession serializes model changes and Job launches.
func (s *Store) LockSession(ctx context.Context, owner domain.UserID, id domain.SessionID) (domain.Session, error) {
	return scanSession(s.q.QueryRow(ctx, `SELECT `+sessionColumns+` FROM sessions s JOIN projects p ON p.id=s.project_id WHERE s.id=$1 AND p.owner_id=$2 FOR UPDATE OF s`, id, owner))
}

func (s *Store) SetSessionModel(ctx context.Context, owner domain.UserID, id domain.SessionID, model, effort string) (domain.Session, error) {
	return scanSession(s.q.QueryRow(ctx, `UPDATE sessions s SET model=$3, effort=$4, updated_at=now() FROM projects p WHERE s.project_id=p.id AND s.id=$1 AND p.owner_id=$2 RETURNING `+sessionColumns, id, owner, model, effort))
}

// SetSessionManager attaches the session to a manager, or detaches it with nil.
func (s *Store) SetSessionManager(ctx context.Context, owner domain.UserID, id domain.SessionID, manager *domain.SessionID) (domain.Session, error) {
	return scanSession(s.q.QueryRow(ctx, `UPDATE sessions s SET manager_session_id=$3, updated_at=now() FROM projects p WHERE s.project_id=p.id AND s.id=$1 AND p.owner_id=$2 RETURNING `+sessionColumns, id, owner, manager))
}

func (s *Store) SetJobModel(ctx context.Context, id domain.JobID, model, effort string) error {
	_, err := s.q.Exec(ctx, `UPDATE jobs SET model=$2, effort=$3 WHERE id=$1`, id, model, effort)
	return classify(err, "record job model")
}

func (s *Store) SetBackendModels(ctx context.Context, id domain.BackendInstanceID, catalog *domain.ModelCatalog) error {
	data, err := json.Marshal(catalog)
	if err != nil {
		return err
	}
	_, err = s.q.Exec(ctx, `UPDATE backend_instances SET model_catalog=$2 WHERE id=$1`, id, data)
	return classify(err, "record backend models")
}
