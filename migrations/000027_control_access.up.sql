CREATE TABLE control_accesses (
 id UUID PRIMARY KEY,
 owner_id TEXT NOT NULL,
 label TEXT NOT NULL,
 secret_hash TEXT NOT NULL UNIQUE,
 scopes TEXT[] NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 expires_at TIMESTAMPTZ,
 revoked_at TIMESTAMPTZ,
 bootstrap BOOLEAN NOT NULL DEFAULT false,
 CHECK (cardinality(scopes) > 0 AND scopes <@ ARRAY['read','manage','answer','approve']::text[])
);
CREATE INDEX control_access_owner ON control_accesses(owner_id);
CREATE UNIQUE INDEX control_access_bootstrap ON control_accesses(owner_id) WHERE bootstrap;
