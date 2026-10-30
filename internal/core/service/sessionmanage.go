package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/events"
	"github.com/rclsilver/threavia/internal/core/storage/postgres"
)

// maxManagerDepth bounds the walk up a chain of managers when looking for a cycle.
const maxManagerDepth = 32

// optionalString reads a string input that may be absent; present means the
// caller set it, even to the empty string that selects a default.
func presentString(input map[string]any, key string) (*string, error) {
	raw, present := input[key]
	if !present {
		return nil, nil
	}
	value, ok := raw.(string)
	if !ok {
		return nil, fmt.Errorf("%w: %s must be a string", ErrInvalid, key)
	}
	return &value, nil
}

// updateManagedSession changes where and how the next jobs of a delegate run.
func (s *Service) updateManagedSession(ctx context.Context, identity auth.Identity, session domain.Session, input map[string]any) (map[string]any, error) {
	backend, err := presentString(input, "backendInstanceId")
	if err != nil {
		return nil, err
	}
	model, err := presentString(input, "model")
	if err != nil {
		return nil, err
	}
	effort, err := presentString(input, "effort")
	if err != nil {
		return nil, err
	}
	if backend == nil && model == nil && effort == nil {
		return nil, fmt.Errorf("%w: give backendInstanceId, model or effort to change", ErrInvalid)
	}
	run, err := s.store.LatestRun(ctx, session.ID)
	if err != nil {
		return nil, translate(err)
	}
	result := map[string]any{}
	if backend != nil && domain.BackendInstanceID(strings.TrimSpace(*backend)) != run.BackendInstanceID {
		id := domain.BackendInstanceID(strings.TrimSpace(*backend))
		handoff, err := s.moveSessionToBackend(ctx, identity, session.ID, id, run.ID, model, effort)
		if err != nil {
			return nil, err
		}
		result["run"] = handoff.Run
		if len(handoff.MissingSkills) > 0 {
			result["missingSkills"] = handoff.MissingSkills
		}
	} else if model != nil || effort != nil {
		if _, err := s.SetSessionModel(ctx, identity, session.ID, model, effort); err != nil {
			return nil, err
		}
	}
	updated, err := s.store.GetSession(ctx, identity.UserID, session.ID)
	if err != nil {
		return nil, translate(err)
	}
	result["session"] = updated
	return sessionToolResult(result)
}

// detachSession turns a delegate back into an ordinary session.
func (s *Service) detachSession(ctx context.Context, identity auth.Identity, jc jobScope, session domain.Session) (map[string]any, error) {
	return s.changeSessionManager(ctx, identity, jc, session.ID, false)
}

// adoptSession makes an unmanaged session a delegate of this conversation.
func (s *Service) adoptSession(ctx context.Context, identity auth.Identity, jc jobScope, session domain.Session) (map[string]any, error) {
	result, err := s.changeSessionManager(ctx, identity, jc, session.ID, true)
	if err != nil {
		return nil, err
	}
	// Questions and permissions already waiting in the session now belong to
	// its new manager.
	s.wakeManagedAttention(ctx, identity.UserID, jc.SessionID)
	return result, nil
}

func (s *Service) changeSessionManager(ctx context.Context, identity auth.Identity, jc jobScope, id domain.SessionID, adopt bool) (map[string]any, error) {
	var result domain.Session
	err := s.store.WithTx(ctx, func(tx *postgres.Store) error {
		session, err := tx.LockSession(ctx, identity.UserID, id)
		if err != nil {
			return err
		}
		owned := session.ManagerSessionID != nil && *session.ManagerSessionID == jc.SessionID
		switch {
		case adopt && owned, !adopt && session.ManagerSessionID == nil:
			result = session
			return nil
		case !adopt && !owned:
			return fmt.Errorf("%w: the session is managed by another conversation", ErrConflict)
		case adopt && session.ManagerSessionID != nil:
			return fmt.Errorf("%w: the session is already managed by another conversation; its manager must detach it first", ErrConflict)
		case adopt && session.Status != domain.SessionActive:
			return fmt.Errorf("%w: the session is archived", ErrConflict)
		case adopt && id == jc.SessionID:
			return fmt.Errorf("%w: a conversation cannot manage itself", ErrInvalid)
		}
		var manager *domain.SessionID
		if adopt {
			manager = &jc.SessionID
			// Refuse a cycle: this conversation must not be (indirectly)
			// managed by the session it adopts.
			cursor := jc.SessionID
			for depth := 0; depth < maxManagerDepth; depth++ {
				current, err := tx.GetSession(ctx, identity.UserID, cursor)
				if err != nil {
					return err
				}
				if current.ManagerSessionID == nil {
					break
				}
				if *current.ManagerSessionID == id {
					return fmt.Errorf("%w: this conversation is managed by that session", ErrInvalid)
				}
				cursor = *current.ManagerSessionID
			}
		}
		result, err = tx.SetSessionManager(ctx, identity.UserID, id, manager)
		if err != nil {
			return err
		}
		kind := events.Type("session.manager_detached")
		if adopt {
			kind = "session.manager_adopted"
		}
		return s.recordLifecycleAudit(ctx, tx, identity, kind,
			domain.Scope{ProjectID: session.ProjectID, SessionID: id}, string(id),
			map[string]any{"managerSessionId": jc.SessionID})
	})
	if err != nil {
		return nil, translate(err)
	}
	return sessionToolResult(map[string]any{"session": result})
}
