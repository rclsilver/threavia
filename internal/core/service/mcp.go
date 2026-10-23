package service

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/events"
	"github.com/rclsilver/threavia/internal/core/storage/postgres"
	"github.com/rclsilver/threavia/pkg/mcpconfig"
	"google.golang.org/protobuf/types/known/structpb"
)

func (s *Service) ListMCP(ctx context.Context, i auth.Identity) ([]mcpconfig.Server, error) {
	v, e := s.store.ListMCP(ctx, i.UserID)
	return v, translate(e)
}
func (s *Service) SaveMCP(ctx context.Context, i auth.Identity, v mcpconfig.Server, create bool) (mcpconfig.Server, error) {
	v.ProjectIDs = nil
	if v.Tools == nil {
		v.Tools = []string{}
	}
	if v.ReadTools == nil {
		v.ReadTools = []string{}
	}
	if err := v.Validate(); err != nil {
		return v, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if create {
		v.ID = domain.NewUUID()
		v.Revision = 1
	} else if _, err := s.store.GetMCP(ctx, i.UserID, v.ID); err != nil {
		return v, translate(err)
	} else {
		if v.Revision < 1 {
			return v, fmt.Errorf("%w: reload the declaration and include its revision", ErrConflict)
		}
		v.Revision++
	}
	b := &batch{ownerID: i.UserID}
	err := s.store.WithTx(ctx, func(tx *postgres.Store) error {
		if err := tx.LockMCP(ctx, i.UserID); err != nil {
			return err
		}
		if err := tx.SaveMCP(ctx, i.UserID, v, create); err != nil {
			return err
		}
		return s.recordMCP(ctx, tx, b, i, map[string]any{"serverId": v.ID, "name": v.Name, "action": "saved"})
	})
	if err != nil {
		return v, translate(err)
	}
	s.flush(b)
	return v, nil
}
func (s *Service) DeleteMCP(ctx context.Context, i auth.Identity, id string) error {
	b := &batch{ownerID: i.UserID}
	err := s.store.WithTx(ctx, func(tx *postgres.Store) error {
		if err := tx.LockMCP(ctx, i.UserID); err != nil {
			return err
		}
		v, err := tx.GetMCP(ctx, i.UserID, id)
		if err != nil {
			return err
		}
		if err := tx.DeleteMCP(ctx, i.UserID, id); err != nil {
			return err
		}
		return s.recordMCP(ctx, tx, b, i, map[string]any{"serverId": id, "name": v.Name, "action": "deleted"})
	})
	if err != nil {
		return translate(err)
	}
	s.flush(b)
	return nil
}
func (s *Service) recordMCP(ctx context.Context, tx *postgres.Store, b *batch, i auth.Identity, p map[string]any) error {
	subject := ""
	for _, key := range []string{"serverId", "sessionId", "projectId"} {
		if value, ok := p[key]; ok {
			subject = fmt.Sprint(value)
			break
		}
	}
	if err := s.recordLifecycleAudit(ctx, tx, i, events.TypeMCPChanged, domain.Scope{}, subject, p); err != nil {
		return err
	}
	return s.appendAll(ctx, tx, b, record{eventType: events.TypeMCPChanged, payload: p})
}
func (s *Service) MCPBindings(ctx context.Context, i auth.Identity, project domain.ProjectID) ([]mcpconfig.Binding, error) {
	if _, err := s.store.GetProject(ctx, i.UserID, project); err != nil {
		return nil, translate(err)
	}
	v, e := s.store.MCPBindings(ctx, i.UserID, project)
	return v, translate(e)
}
func (s *Service) BindMCP(ctx context.Context, i auth.Identity, project domain.ProjectID, v mcpconfig.Binding) error {
	b := &batch{ownerID: i.UserID}
	err := s.store.WithTx(ctx, func(tx *postgres.Store) error {
		if err := tx.LockMCP(ctx, i.UserID); err != nil {
			return err
		}
		if err := tx.SetMCPBinding(ctx, i.UserID, project, v); err != nil {
			return err
		}
		return s.recordMCP(ctx, tx, b, i, map[string]any{"serverId": v.ServerID, "projectId": project, "enabled": v.Enabled, "required": v.Required, "action": "bound"})
	})
	if err != nil {
		return translate(err)
	}
	s.flush(b)
	return nil
}
func (s *Service) MCPRestriction(ctx context.Context, i auth.Identity, session domain.SessionID) (map[string]any, error) {
	if _, err := s.store.GetSession(ctx, i.UserID, session); err != nil {
		return nil, translate(err)
	}
	ids, set, err := s.store.MCPRestriction(ctx, session)
	return map[string]any{"restricted": set, "serverIds": ids}, translate(err)
}
func (s *Service) RestrictMCP(ctx context.Context, i auth.Identity, session domain.SessionID, ids []string) error {
	sess, err := s.store.GetSession(ctx, i.UserID, session)
	if err != nil {
		return translate(err)
	}
	bindings, err := s.store.MCPBindings(ctx, i.UserID, sess.ProjectID)
	if err != nil {
		return translate(err)
	}
	for _, id := range ids {
		if !slices.ContainsFunc(bindings, func(b mcpconfig.Binding) bool { return b.ServerID == id && b.Enabled }) {
			return fmt.Errorf("%w: session may only select project-enabled servers", ErrInvalid)
		}
	}
	b := &batch{ownerID: i.UserID}
	err = s.store.WithTx(ctx, func(tx *postgres.Store) error {
		if err := tx.LockMCP(ctx, i.UserID); err != nil {
			return err
		}
		if err := tx.SetMCPRestriction(ctx, session, ids); err != nil {
			return err
		}
		return s.recordMCP(ctx, tx, b, i, map[string]any{"sessionId": session, "action": "restricted"})
	})
	if err != nil {
		return translate(err)
	}
	s.flush(b)
	return nil
}
func (s *Service) effectiveMCP(ctx context.Context, store *postgres.Store, jc postgres.JobContext) ([]*structpb.Struct, error) {
	raw, err := store.MCPJobSnapshot(ctx, jc.Job.ID)
	if err != nil {
		return nil, err
	}
	if raw != nil {
		var saved []map[string]any
		if err := json.Unmarshal(raw, &saved); err != nil {
			return nil, err
		}
		out := make([]*structpb.Struct, 0, len(saved))
		for _, v := range saved {
			st, err := structpb.NewStruct(v)
			if err != nil {
				return nil, err
			}
			out = append(out, st)
		}
		return out, nil
	}
	all, err := store.ListMCP(ctx, jc.OwnerID)
	if err != nil {
		return nil, err
	}
	bindings, err := store.MCPBindings(ctx, jc.OwnerID, jc.ProjectID)
	if err != nil {
		return nil, err
	}
	ids, restricted, err := store.MCPRestriction(ctx, jc.SessionID)
	if err != nil {
		return nil, err
	}
	var out []*structpb.Struct
	for _, b := range bindings {
		if !b.Enabled || (restricted && !slices.Contains(ids, b.ServerID)) {
			continue
		}
		for _, v := range all {
			if v.ID != b.ServerID {
				continue
			}
			if !v.Enabled {
				if b.Required {
					return nil, fmt.Errorf("%w: required MCP server %s is disabled", ErrInvalid, v.Name)
				}
				continue
			}
			data, _ := json.Marshal(mcpconfig.Effective{Server: v, Required: b.Required})
			st := &structpb.Struct{}
			if err := st.UnmarshalJSON(data); err != nil {
				return nil, err
			}
			out = append(out, st)
		}
	}
	if out == nil {
		out = []*structpb.Struct{}
	}
	snapshot, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	if err := store.SaveMCPJobSnapshot(ctx, jc.Job.ID, snapshot); err != nil {
		return nil, err
	}
	return out, nil
}

type mcpWait struct {
	backend domain.BackendInstanceID
	answer  chan *backendv1.McpResponse
}

func (s *Service) McpResponse(_ context.Context, id domain.BackendInstanceID, r *backendv1.McpResponse) {
	v, ok := s.mcpRequests.Load(r.RequestId)
	if !ok {
		return
	}
	w := v.(mcpWait)
	if w.backend != id {
		return
	}
	select {
	case w.answer <- r:
	default:
	}
}

// MCPBackend forwards explicit owner operations; it never accepts secret values.
func (s *Service) MCPBackend(ctx context.Context, i auth.Identity, backend domain.BackendInstanceID, operation, serverID string, input map[string]any) (map[string]any, error) {
	b, err := s.store.GetBackendInstance(ctx, i.UserID, backend)
	if err != nil {
		return nil, translate(err)
	}
	if !b.HasFeature(domain.FeatureExternalMCP) {
		return nil, fmt.Errorf("%w: update this backend to manage external MCP servers", ErrInvalid)
	}
	if operation != "probe" && operation != "auth_start" && operation != "auth_finish" && operation != "logout" && operation != "import" {
		return nil, fmt.Errorf("%w: unknown MCP operation", ErrInvalid)
	}
	payload := map[string]any{}
	if operation == "import" {
		payload["source"] = input["source"]
	} else {
		v, err := s.store.GetMCP(ctx, i.UserID, serverID)
		if err != nil {
			return nil, translate(err)
		}
		data, _ := json.Marshal(v)
		_ = json.Unmarshal(data, &payload)
	}
	// Only auth codes/state cross this channel transiently; they are not persisted.
	if operation == "auth_finish" {
		payload["response"] = input["response"]
		payload["state"] = input["state"]
	}
	if operation == "auth_start" {
		payload["redirectUri"] = input["redirectUri"]
	}
	conn, ok := s.backends.Lookup(backend)
	if !ok {
		return nil, ErrBackendUnavailable
	}
	st, err := structpb.NewStruct(payload)
	if err != nil {
		return nil, err
	}
	id := domain.NewUUID()
	w := mcpWait{backend: backend, answer: make(chan *backendv1.McpResponse, 1)}
	s.mcpRequests.Store(id, w)
	defer s.mcpRequests.Delete(id)
	if !conn.Send(&backendv1.CoreToBackend{Message: &backendv1.CoreToBackend_McpRequest{McpRequest: &backendv1.McpRequest{RequestId: id, Operation: operation, Input: st}}}) {
		return nil, ErrBackendUnavailable
	}
	timer := time.NewTimer(45 * time.Second)
	defer timer.Stop()
	select {
	case r := <-w.answer:
		if r.Error != nil {
			return nil, fmt.Errorf("%w: %s", ErrConflict, r.Error.Message)
		}
		return r.GetResult().AsMap(), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, fmt.Errorf("%w: MCP operation timed out", ErrBackendUnavailable)
	}
}
