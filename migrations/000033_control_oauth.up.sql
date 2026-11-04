-- OAuth for the public MCP endpoint (decided 2026-11-04), so that clients
-- such as ChatGPT connect with a consent page instead of a pasted token.
-- Core is the authorization server: public clients register themselves
-- (RFC 7591), the person approves scopes in the Web client, and the grant is
-- a control access like any other, listed and revoked in Settings.

-- Registered clients hold no secret: they are public and prove each code
-- exchange with PKCE.
CREATE TABLE control_oauth_clients (
 id UUID PRIMARY KEY,
 name TEXT NOT NULL,
 redirect_uris TEXT[] NOT NULL CHECK (cardinality(redirect_uris) BETWEEN 1 AND 10),
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);

-- An OAuth grant has no static secret; its tokens live below.
ALTER TABLE control_accesses
 ALTER COLUMN secret_hash DROP NOT NULL,
 ADD COLUMN oauth_client_id UUID REFERENCES control_oauth_clients(id) ON DELETE SET NULL;

-- Authorization codes are single use and short lived; only their digest is kept.
CREATE TABLE control_oauth_codes (
 code_hash TEXT PRIMARY KEY,
 client_id UUID NOT NULL REFERENCES control_oauth_clients(id) ON DELETE CASCADE,
 owner_id TEXT NOT NULL,
 redirect_uri TEXT NOT NULL,
 code_challenge TEXT NOT NULL,
 scopes TEXT[] NOT NULL CHECK (cardinality(scopes) > 0 AND scopes <@ ARRAY['read','manage','answer','approve']::text[]),
 resource TEXT NOT NULL DEFAULT '',
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 expires_at TIMESTAMPTZ NOT NULL,
 used_at TIMESTAMPTZ
);
CREATE INDEX control_oauth_code_expiry ON control_oauth_codes(expires_at);

-- Access and refresh tokens of a grant, by digest. Revoking the grant ends
-- them all; a refresh rotates both.
CREATE TABLE control_oauth_tokens (
 token_hash TEXT PRIMARY KEY,
 access_id UUID NOT NULL REFERENCES control_accesses(id) ON DELETE CASCADE,
 kind TEXT NOT NULL CHECK (kind IN ('access', 'refresh')),
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 expires_at TIMESTAMPTZ NOT NULL,
 used_at TIMESTAMPTZ
);
CREATE INDEX control_oauth_token_access ON control_oauth_tokens(access_id);
CREATE INDEX control_oauth_token_expiry ON control_oauth_tokens(expires_at);
