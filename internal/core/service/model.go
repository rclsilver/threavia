package service

import (
	"context"
	"fmt"

	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/events"
	"github.com/rclsilver/threavia/internal/core/storage/postgres"
)

type ModelPayload struct {
	Model  string `json:"model"`
	Effort string `json:"effort"`
}

func (s *Service) SetSessionModel(ctx context.Context, identity auth.Identity, id domain.SessionID, model, effort *string) (domain.Session, error) {
	var result domain.Session
	b := &batch{ownerID: identity.UserID}
	err := s.store.WithTx(ctx, func(tx *postgres.Store) error {
		session, err := tx.LockSession(ctx, identity.UserID, id)
		if err != nil {
			return err
		}
		selected, reasoning := session.Model, session.Effort
		if model != nil {
			selected = *model
		}
		if effort != nil {
			reasoning = *effort
		}
		run, err := tx.LatestRun(ctx, id)
		if err != nil {
			return err
		}
		backend, err := tx.GetBackendInstance(ctx, identity.UserID, run.BackendInstanceID)
		if err != nil {
			return err
		}
		if _, _, err = backend.ResolveModel(selected, reasoning); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		result = session
		if selected == session.Model && reasoning == session.Effort {
			return nil
		}
		result, err = tx.SetSessionModel(ctx, identity.UserID, id, selected, reasoning)
		if err != nil {
			return err
		}
		return s.appendAll(ctx, tx, b, record{events.TypeSessionModelChanged, domain.Scope{ProjectID: session.ProjectID, SessionID: id}, ModelPayload{selected, reasoning}})
	})
	if err != nil {
		return domain.Session{}, translate(err)
	}
	s.flush(b)
	if len(b.records) > 0 {
		s.audit(ctx, identity, postgres.AuditEntry{Action: "session.model_changed", ProjectID: string(result.ProjectID), SessionID: string(id), SubjectID: string(id), Detail: mustJSON(ModelPayload{result.Model, result.Effort})})
	}
	return result, nil
}
