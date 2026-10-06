-- Tables required by the first vertical slice beyond the core hierarchy
-- (THREAVIA_SPEC_V1.md sections 11, 16 and 30): logical directories and their
-- per-backend bindings, the two persistent attention objects, and the backend
-- credentials the registration flows of section 8 issue.

-- A project-level logical directory. Git repositories and worktrees are
-- deliberately not Core objects (section 11).
CREATE TABLE known_directories (
    id          UUID        PRIMARY KEY,
    project_id  UUID        NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    name        TEXT        NOT NULL,
    description TEXT        NOT NULL DEFAULT '',
    git_remote  TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT known_directories_name_not_empty CHECK (length(btrim(name)) > 0)
);

CREATE UNIQUE INDEX known_directories_project_name_key ON known_directories (project_id, name);

-- The same logical directory lives at different physical paths on different
-- backends. The path is backend truth, never a Core sandbox.
CREATE TABLE known_directory_bindings (
    known_directory_id  UUID        NOT NULL REFERENCES known_directories (id) ON DELETE CASCADE,
    backend_instance_id UUID        NOT NULL REFERENCES backend_instances (id) ON DELETE CASCADE,
    path                TEXT        NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),

    PRIMARY KEY (known_directory_id, backend_instance_id),
    CONSTRAINT known_directory_bindings_path_absolute CHECK (path LIKE '/%')
);

-- The Session working directory is a KnownDirectory once that table exists.
ALTER TABLE sessions
    ADD CONSTRAINT sessions_working_directory_fkey
    FOREIGN KEY (working_directory_id) REFERENCES known_directories (id) ON DELETE SET NULL;

-- Persistent permission/approval request (section 16). Pending attention is
-- current state, not an unread-event counter.
CREATE TABLE validation_requests (
    id         UUID NOT NULL PRIMARY KEY,
    project_id UUID NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    session_id UUID NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    run_id     UUID NOT NULL REFERENCES runs (id) ON DELETE CASCADE,
    job_id     UUID NOT NULL REFERENCES jobs (id) ON DELETE CASCADE,
    -- Backend-scoped id, so a replayed event resolves to the same request.
    backend_request_id TEXT NOT NULL,

    status    TEXT NOT NULL DEFAULT 'PENDING',
    title     TEXT NOT NULL DEFAULT '',
    summary   TEXT NOT NULL DEFAULT '',
    -- Canonical technical payload and its SHA-256: the security reference of
    -- the validation receipt.
    request_payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    payload_sha256  TEXT  NOT NULL,
    -- Optional humanized presentation, for audit convenience only.
    humanized TEXT NOT NULL DEFAULT '',

    approved            BOOLEAN,
    resolved_by_user_id TEXT,
    resolved_channel    TEXT,
    note                TEXT,

    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at TIMESTAMPTZ,

    CONSTRAINT validation_requests_status_check CHECK (status IN ('PENDING', 'RESOLVED')),
    CONSTRAINT validation_requests_resolved_shape CHECK (
        (status = 'PENDING'  AND approved IS NULL     AND resolved_at IS NULL) OR
        (status = 'RESOLVED' AND approved IS NOT NULL AND resolved_at IS NOT NULL)
    )
);

CREATE UNIQUE INDEX validation_requests_backend_key ON validation_requests (job_id, backend_request_id);
CREATE INDEX validation_requests_pending_idx ON validation_requests (session_id) WHERE status = 'PENDING';

-- Persistent request for an answer, a choice or information. Distinct from a
-- validation: permission versus information.
CREATE TABLE user_input_requests (
    id         UUID NOT NULL PRIMARY KEY,
    project_id UUID NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    session_id UUID NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    run_id     UUID NOT NULL REFERENCES runs (id) ON DELETE CASCADE,
    job_id     UUID NOT NULL REFERENCES jobs (id) ON DELETE CASCADE,
    backend_request_id TEXT NOT NULL,

    status    TEXT   NOT NULL DEFAULT 'PENDING',
    prompt    TEXT   NOT NULL DEFAULT '',
    choices   TEXT[] NOT NULL DEFAULT '{}',
    free_text BOOLEAN NOT NULL DEFAULT TRUE,

    value               TEXT,
    resolved_by_user_id TEXT,
    resolved_channel    TEXT,

    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at TIMESTAMPTZ,

    CONSTRAINT user_input_requests_status_check CHECK (status IN ('PENDING', 'RESOLVED')),
    CONSTRAINT user_input_requests_resolved_shape CHECK (
        (status = 'PENDING'  AND value IS NULL     AND resolved_at IS NULL) OR
        (status = 'RESOLVED' AND value IS NOT NULL AND resolved_at IS NOT NULL)
    )
);

CREATE UNIQUE INDEX user_input_requests_backend_key ON user_input_requests (job_id, backend_request_id);
CREATE INDEX user_input_requests_pending_idx ON user_input_requests (session_id) WHERE status = 'PENDING';

-- Persistent backend credential issued at registration. Only its hash is
-- stored: Core can verify a presented credential but never reproduce it.
ALTER TABLE backend_instances
    ADD COLUMN credential_sha256 TEXT,
    -- One-time claim code of the shared-key registration flow, also hashed.
    ADD COLUMN claim_code_sha256 TEXT,
    ADD COLUMN claim_code_expires_at TIMESTAMPTZ;

CREATE UNIQUE INDEX backend_instances_credential_key
    ON backend_instances (credential_sha256)
    WHERE credential_sha256 IS NOT NULL;

-- One-shot registration token a user creates ahead of time (section 8). The
-- backend registering with it is immediately owned, with no claim step.
CREATE TABLE backend_registration_tokens (
    id           UUID        PRIMARY KEY,
    owner_id     TEXT        NOT NULL,
    token_sha256 TEXT        NOT NULL,
    label        TEXT        NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at   TIMESTAMPTZ NOT NULL,
    used_at      TIMESTAMPTZ,
    -- Written in the same transaction as the instance it points at, in either
    -- order, so the reference is checked at commit rather than per statement.
    backend_instance_id UUID REFERENCES backend_instances (id) ON DELETE SET NULL
        DEFERRABLE INITIALLY DEFERRED,

    CONSTRAINT backend_registration_tokens_used_shape CHECK (
        (used_at IS NULL AND backend_instance_id IS NULL) OR
        (used_at IS NOT NULL AND backend_instance_id IS NOT NULL)
    )
);

CREATE UNIQUE INDEX backend_registration_tokens_token_key ON backend_registration_tokens (token_sha256);
CREATE INDEX backend_registration_tokens_owner_idx ON backend_registration_tokens (owner_id);
