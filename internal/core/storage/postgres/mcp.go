package postgres

import (
	"context"
	"encoding/json"

	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/pkg/mcpconfig"
)

// LockMCP serializes registry changes and Job snapshots for one owner. Call in a transaction.
func (s *Store) LockMCP(ctx context.Context, owner domain.UserID) error {
	_, err := s.q.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,26))`, string(owner))
	return err
}

func (s *Store) MCPJobSnapshot(ctx context.Context, job domain.JobID) (json.RawMessage, error) {
	var raw []byte
	err := s.q.QueryRow(ctx, `SELECT mcp_snapshot FROM jobs WHERE id=$1`, job).Scan(&raw)
	return raw, classify(err, "read MCP Job snapshot")
}
func (s *Store) SaveMCPJobSnapshot(ctx context.Context, job domain.JobID, raw []byte) error {
	_, err := s.q.Exec(ctx, `UPDATE jobs SET mcp_snapshot=$2 WHERE id=$1 AND mcp_snapshot IS NULL`, job, raw)
	return err
}

func (s *Store) ListMCP(ctx context.Context, owner domain.UserID) ([]mcpconfig.Server, error) {
	rows, err := s.q.Query(ctx, `SELECT declaration,COALESCE((SELECT jsonb_agg(project_id::text ORDER BY project_id) FROM mcp_bindings WHERE server_id=mcp_servers.id),'[]') FROM mcp_servers WHERE owner_id=$1 ORDER BY name`, owner)
	if err != nil {
		return nil, classify(err, "list MCP servers")
	}
	defer rows.Close()
	out := []mcpconfig.Server{}
	for rows.Next() {
		var data []byte
		var projects []byte
		var v mcpconfig.Server
		if err := rows.Scan(&data, &projects); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(data, &v); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(projects, &v.ProjectIDs); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) GetMCP(ctx context.Context, owner domain.UserID, id string) (mcpconfig.Server, error) {
	var data []byte
	err := s.q.QueryRow(ctx, `SELECT declaration FROM mcp_servers WHERE owner_id=$1 AND id=$2`, owner, id).Scan(&data)
	var v mcpconfig.Server
	if err != nil {
		return v, classify(err, "read MCP server")
	}
	err = json.Unmarshal(data, &v)
	return v, err
}
func (s *Store) SaveMCP(ctx context.Context, owner domain.UserID, v mcpconfig.Server, create bool) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if create {
		_, err = s.q.Exec(ctx, `INSERT INTO mcp_servers(id,owner_id,name,declaration)VALUES($1,$2,$3,$4)`, v.ID, owner, v.Name, data)
	} else {
		var id string
		err = s.q.QueryRow(ctx, `UPDATE mcp_servers SET name=$3,declaration=$4,revision=revision+1,updated_at=now() WHERE id=$1 AND owner_id=$2 AND revision=$5 RETURNING id`, v.ID, owner, v.Name, data, v.Revision-1).Scan(&id)
		if classify(err, "save MCP revision") == ErrNotFound {
			return ErrConflict
		}
	}
	return classify(err, "save MCP server")
}
func (s *Store) DeleteMCP(ctx context.Context, owner domain.UserID, id string) error {
	tag, err := s.q.Exec(ctx, `DELETE FROM mcp_servers WHERE owner_id=$1 AND id=$2`, owner, id)
	if err != nil {
		return classify(err, "delete MCP server")
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
func (s *Store) MCPBindings(ctx context.Context, owner domain.UserID, project domain.ProjectID) ([]mcpconfig.Binding, error) {
	rows, err := s.q.Query(ctx, `SELECT b.server_id,b.enabled,b.required FROM mcp_bindings b JOIN mcp_servers m ON m.id=b.server_id JOIN projects p ON p.id=b.project_id WHERE m.owner_id=$1 AND p.owner_id=$1 AND p.id=$2 ORDER BY b.server_id`, owner, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []mcpconfig.Binding{}
	for rows.Next() {
		var b mcpconfig.Binding
		if err := rows.Scan(&b.ServerID, &b.Enabled, &b.Required); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
func (s *Store) SetMCPBinding(ctx context.Context, owner domain.UserID, project domain.ProjectID, b mcpconfig.Binding) error {
	// The SELECT prevents a binding between objects owned by different people.
	tag, err := s.q.Exec(ctx, `INSERT INTO mcp_bindings(server_id,project_id,enabled,required) SELECT m.id,p.id,$4,$5 FROM mcp_servers m,projects p WHERE m.id=$2 AND m.owner_id=$1 AND p.id=$3 AND p.owner_id=$1 ON CONFLICT(server_id,project_id) DO UPDATE SET enabled=EXCLUDED.enabled,required=EXCLUDED.required`, owner, b.ServerID, project, b.Enabled, b.Required)
	if err != nil {
		return classify(err, "bind MCP server")
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
func (s *Store) MCPRestriction(ctx context.Context, session domain.SessionID) ([]string, bool, error) {
	var data []byte
	err := s.q.QueryRow(ctx, `SELECT server_ids FROM mcp_session_restrictions WHERE session_id=$1`, session).Scan(&data)
	if classify(err, "read MCP restriction") == ErrNotFound {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var ids []string
	err = json.Unmarshal(data, &ids)
	return ids, true, err
}
func (s *Store) SetMCPRestriction(ctx context.Context, session domain.SessionID, ids []string) error {
	if ids == nil {
		_, err := s.q.Exec(ctx, `DELETE FROM mcp_session_restrictions WHERE session_id=$1`, session)
		return err
	}
	data, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	_, err = s.q.Exec(ctx, `INSERT INTO mcp_session_restrictions(session_id,server_ids)VALUES($1,$2)ON CONFLICT(session_id)DO UPDATE SET server_ids=EXCLUDED.server_ids`, session, data)
	return err
}
