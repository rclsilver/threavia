package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/events"
	"github.com/rclsilver/threavia/internal/core/storage/postgres"
	"strings"
)

func controlHash(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

func (s *Service) CreateControlAccess(ctx context.Context, i auth.Identity, a domain.ControlAccess) (domain.ControlAccess, string, error) {
	a.ID = domain.NewUUID()
	a.OwnerID = i.UserID
	a.Bootstrap = false
	a.RevokedAt = nil
	if err := a.Validate(); err != nil {
		return a, "", fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if a.ExpiresAt != nil && !a.ExpiresAt.After(s.now()) {
		return a, "", fmt.Errorf("%w: expiry must be in the future", ErrInvalid)
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return a, "", err
	}
	token := "threavia_" + base64.RawURLEncoding.EncodeToString(secret)
	var saved domain.ControlAccess
	b := &batch{ownerID: i.UserID}
	err := s.store.WithTx(ctx, func(tx *postgres.Store) error {
		var err error
		saved, err = tx.CreateControlAccess(ctx, a, controlHash(token))
		if err != nil {
			return err
		}
		if err := tx.RecordAudit(ctx, postgres.AuditEntry{OwnerID: i.UserID, ActorID: string(i.UserID), Action: "control_access.created", SubjectID: a.ID, Channel: domain.ChannelFrom(ctx).String(), Detail: mustJSON(map[string]any{"label": a.Label, "scopes": a.Scopes})}); err != nil {
			return err
		}
		return s.appendAll(ctx, tx, b, record{eventType: events.TypeControlAccessChanged, payload: map[string]any{"accessId": a.ID, "action": "created"}})
	})
	if err != nil {
		return a, "", translate(err)
	}
	s.flush(b)
	return saved, token, nil
}
func (s *Service) ListControlAccesses(ctx context.Context, i auth.Identity) ([]domain.ControlAccess, error) {
	a, e := s.store.ListControlAccesses(ctx, i.UserID)
	return a, translate(e)
}
func (s *Service) RevokeControlAccess(ctx context.Context, i auth.Identity, id string) error {
	b := &batch{ownerID: i.UserID}
	err := s.store.WithTx(ctx, func(tx *postgres.Store) error {
		if err := tx.RevokeControlAccess(ctx, i.UserID, id); err != nil {
			return err
		}
		if err := tx.RecordAudit(ctx, postgres.AuditEntry{OwnerID: i.UserID, ActorID: string(i.UserID), Action: "control_access.revoked", SubjectID: id, Channel: domain.ChannelFrom(ctx).String()}); err != nil {
			return err
		}
		return s.appendAll(ctx, tx, b, record{eventType: events.TypeControlAccessChanged, payload: map[string]any{"accessId": id, "action": "revoked"}})
	})
	if err != nil {
		return translate(err)
	}
	s.flush(b)
	return nil
}
func (s *Service) AuthenticateControl(ctx context.Context, token string) (domain.ControlAccess, error) {
	if len(token) < 32 || len(token) > 256 {
		return domain.ControlAccess{}, auth.ErrUnauthenticated
	}
	a, e := s.store.ControlAccessByHash(ctx, controlHash(token))
	if errors.Is(e, postgres.ErrNotFound) {
		// Not a token made in Settings: an access token an OAuth grant issued.
		a, e = s.store.ControlAccessByOAuthToken(ctx, controlHash(token))
	}
	if e != nil {
		return a, auth.ErrUnauthenticated
	}
	return a, nil
}
func (s *Service) ControlAccess(ctx context.Context, owner domain.UserID, id string) (domain.ControlAccess, error) {
	a, e := s.store.ControlAccess(ctx, owner, id)
	return a, translate(e)
}
func (s *Service) BootstrapControlAccess(ctx context.Context, owner, token string, scopes []string) error {
	if token == "" {
		return nil
	}
	if strings.TrimSpace(owner) == "" || len(owner) > 256 || strings.TrimSpace(token) != token || len(token) < 32 || len(token) > 256 {
		return fmt.Errorf("MCP bootstrap requires an owner and a token of 32 to 256 bytes")
	}
	if err := domain.ValidateControlScopes(scopes); err != nil {
		return err
	}
	a := domain.ControlAccess{ID: domain.NewUUID(), OwnerID: domain.UserID(owner), Label: "Environment bootstrap", Scopes: scopes, Bootstrap: true}
	return s.store.WithTx(ctx, func(tx *postgres.Store) error {
		if err := tx.BootstrapControlAccess(ctx, a, controlHash(token)); err != nil {
			return err
		}
		// Audit once, only when this insertion won. No credentials in the audit.
		found, err := tx.ListControlAccesses(ctx, a.OwnerID)
		if err != nil {
			return err
		}
		for _, v := range found {
			if v.ID == a.ID {
				return tx.RecordAudit(ctx, postgres.AuditEntry{OwnerID: a.OwnerID, ActorID: "bootstrap", Action: "control_access.created", SubjectID: a.ID, Channel: "mcp", Detail: mustJSON(map[string]any{"bootstrap": true, "scopes": scopes})})
			}
		}
		return nil
	})
}

// RecordControlCall records only the operation and outcome, never tool arguments
// or returned messages, OAuth responses, or registration credentials.
func (s *Service) RecordControlCall(ctx context.Context, a domain.ControlAccess, name string, ok bool) error {
	return s.store.RecordAudit(ctx, postgres.AuditEntry{OwnerID: a.OwnerID, ActorID: "mcp:" + a.ID, Action: "control.tool", SubjectID: name, Channel: "mcp", Detail: mustJSON(map[string]any{"accessId": a.ID, "tool": name, "success": ok})})
}

func (s *Service) ControlSessionWait(ctx context.Context, i auth.Identity, in map[string]any) (any, error) {
	session, err := s.ownedSession(ctx, i, text(in, "sessionId"))
	if err != nil {
		return nil, err
	}
	return s.waitSession(ctx, i, session, in)
}

func (s *Service) ControlSessionRead(ctx context.Context, i auth.Identity, in map[string]any) (any, error) {
	session, err := s.ownedSession(ctx, i, text(in, "sessionId"))
	if err != nil {
		return nil, err
	}
	before, err := integerInput(in, "before", 0, 0, 1<<53-1)
	if err != nil {
		return nil, err
	}
	limit, err := integerInput(in, "limit", 50, 1, 100)
	if err != nil {
		return nil, err
	}
	return s.readManagedSession(ctx, i, session, domain.Sequence(before), int(limit))
}

func (s *Service) ControlSessionAnswer(ctx context.Context, i auth.Identity, in map[string]any) (any, error) {
	session, err := s.ownedSession(ctx, i, text(in, "sessionId"))
	if err != nil {
		return nil, err
	}
	request, err := s.store.GetUserInputRequest(ctx, i.UserID, domain.UserInputRequestID(text(in, "requestId")))
	if err != nil {
		return nil, translate(err)
	}
	if request.Scope.SessionID != session.ID {
		return nil, fmt.Errorf("%w: question belongs to another session", ErrInvalid)
	}
	value := text(in, "value")
	if strings.TrimSpace(value) == "" {
		return nil, fmt.Errorf("%w: answer is required", ErrInvalid)
	}
	if !request.FreeText {
		found := false
		for _, choice := range request.Choices {
			found = found || choice == value
		}
		if !found {
			return nil, fmt.Errorf("%w: answer must be one of the offered choices", ErrInvalid)
		}
	}
	return s.ResolveUserInput(ctx, i, request.ID, value, "mcp")
}
func (s *Service) ControlResolveValidation(ctx context.Context, i auth.Identity, in map[string]any) (any, error) {
	session, err := s.ownedSession(ctx, i, text(in, "sessionId"))
	if err != nil {
		return nil, err
	}
	request, err := s.store.GetValidationRequest(ctx, i.UserID, domain.ValidationRequestID(text(in, "requestId")))
	if err != nil {
		return nil, translate(err)
	}
	if request.Scope.SessionID != session.ID {
		return nil, fmt.Errorf("%w: permission belongs to another session", ErrInvalid)
	}
	if text(in, "payloadSha256") != request.PayloadSHA256 {
		return nil, fmt.Errorf("%w: copy the original permission payload hash unchanged", ErrInvalid)
	}
	approved, ok := in["approved"].(bool)
	if !ok {
		return nil, fmt.Errorf("%w: approved is required", ErrInvalid)
	}
	if domain.ActorFrom(ctx) == "" {
		return nil, fmt.Errorf("%w: delegated actor is required", ErrInvalid)
	}
	return s.ResolveValidation(ctx, i, request.ID, approved, "mcp", text(in, "note"))
}
