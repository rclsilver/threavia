CREATE TABLE mcp_servers (
 id UUID PRIMARY KEY,
 owner_id TEXT NOT NULL,
 name TEXT NOT NULL,
 declaration JSONB NOT NULL,
 revision BIGINT NOT NULL DEFAULT 1,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(owner_id,name)
);
ALTER TABLE jobs ADD COLUMN mcp_snapshot JSONB;
CREATE TABLE mcp_bindings (
 server_id UUID NOT NULL REFERENCES mcp_servers(id) ON DELETE CASCADE,
 project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
 enabled BOOLEAN NOT NULL DEFAULT true,
 required BOOLEAN NOT NULL DEFAULT false,
 PRIMARY KEY(server_id,project_id)
);
-- A session may restrict project selection; it cannot add a project-disabled server.
CREATE TABLE mcp_session_restrictions (
 session_id UUID PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
 server_ids JSONB NOT NULL
);
