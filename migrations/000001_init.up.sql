-- Initial Threavia Core schema: the minimal relational state required by the
-- first vertical slice (THREAVIA_SPEC_V1.md sections 23, 30 and 35).
--
-- Current state lives in these tables and is never rebuilt by replaying events.
-- Later migrations add validation_requests, user_input_requests,
-- known_directories, decisions, tasks, artifacts, skills and audit_entries.
--
-- Users are not modelled yet: owner_id is an opaque identifier produced by the
-- configured authentication mode (none/basic/oidc), so the schema does not
-- commit to a user storage design before the auth implementation lands.

CREATE TABLE projects (
    id          UUID        PRIMARY KEY,
    owner_id    TEXT        NOT NULL,
    name        TEXT        NOT NULL,
    description TEXT        NOT NULL DEFAULT '',
    status      TEXT        NOT NULL DEFAULT 'ACTIVE',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    archived_at TIMESTAMPTZ,

    CONSTRAINT projects_status_check CHECK (status IN ('ACTIVE', 'ARCHIVED')),
    CONSTRAINT projects_name_not_empty CHECK (length(btrim(name)) > 0)
);

CREATE UNIQUE INDEX projects_owner_name_key ON projects (owner_id, name);
CREATE INDEX projects_owner_status_idx ON projects (owner_id, status, updated_at DESC);

-- A BackendInstance belongs to a User, never to a Project. owner_id is NULL
-- while the instance is UNCLAIMED (shared registration key flow, section 8).
CREATE TABLE backend_instances (
    id                  UUID        PRIMARY KEY,
    owner_id            TEXT,
    name                TEXT        NOT NULL,
    ownership_status    TEXT        NOT NULL DEFAULT 'UNCLAIMED',
    operational_status  TEXT        NOT NULL DEFAULT 'OFFLINE',
    provider_auth_state TEXT        NOT NULL DEFAULT 'AUTHENTICATION_REQUIRED',
    capabilities        TEXT[]      NOT NULL DEFAULT '{}',
    feature_flags       JSONB       NOT NULL DEFAULT '{}'::jsonb,
    max_concurrent_runs INTEGER     NOT NULL DEFAULT 1,
    active_runs         INTEGER     NOT NULL DEFAULT 0,
    protocol_version    INTEGER     NOT NULL DEFAULT 0,
    sdk_name            TEXT        NOT NULL DEFAULT '',
    sdk_version         TEXT        NOT NULL DEFAULT '',
    backend_name        TEXT        NOT NULL DEFAULT '',
    backend_version     TEXT        NOT NULL DEFAULT '',
    -- Lease of the currently active control stream (section 9).
    connection_id       TEXT,
    connected_at        TIMESTAMPTZ,
    last_heartbeat_at   TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at          TIMESTAMPTZ,

    CONSTRAINT backend_instances_ownership_check
        CHECK (ownership_status IN ('UNCLAIMED', 'CLAIMED', 'REVOKED')),
    CONSTRAINT backend_instances_operational_check
        CHECK (operational_status IN ('STARTING', 'READY', 'DEGRADED', 'OFFLINE')),
    CONSTRAINT backend_instances_provider_auth_check
        CHECK (provider_auth_state IN ('AUTHENTICATED', 'AUTHENTICATION_REQUIRED')),
    CONSTRAINT backend_instances_claimed_has_owner
        CHECK (ownership_status <> 'CLAIMED' OR owner_id IS NOT NULL),
    CONSTRAINT backend_instances_capacity_check
        CHECK (max_concurrent_runs >= 0 AND active_runs >= 0)
);

-- Names are unique per owner among live instances; revoked records are kept for
-- historical references and a returning backend registers as a new instance.
CREATE UNIQUE INDEX backend_instances_owner_name_key
    ON backend_instances (owner_id, name)
    WHERE owner_id IS NOT NULL AND ownership_status <> 'REVOKED';
CREATE INDEX backend_instances_owner_idx ON backend_instances (owner_id);

CREATE TABLE sessions (
    id         UUID        PRIMARY KEY,
    project_id UUID        NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    title      TEXT        NOT NULL DEFAULT '',
    status     TEXT        NOT NULL DEFAULT 'ACTIVE',
    -- Optional logical working directory, preferably a KnownDirectory. It is the
    -- initial cwd only, never a filesystem boundary (sections 3.2 and 11). The
    -- foreign key to known_directories is added with that table.
    working_directory_id UUID,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    archived_at TIMESTAMPTZ,

    CONSTRAINT sessions_status_check CHECK (status IN ('ACTIVE', 'ARCHIVED'))
);

CREATE INDEX sessions_project_idx ON sessions (project_id, status, updated_at DESC);

-- A Run binds a Session to one BackendInstance and one provider native session.
-- Deleting a Project cascades to its Sessions and Runs; a BackendInstance is
-- user-owned and is never deleted along with a Project (section 21), hence the
-- RESTRICT.
CREATE TABLE runs (
    id                  UUID        PRIMARY KEY,
    session_id          UUID        NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    backend_instance_id UUID        NOT NULL REFERENCES backend_instances (id) ON DELETE RESTRICT,
    native_session_id   TEXT,
    resume_status       TEXT        NOT NULL DEFAULT 'UNKNOWN',
    resume_reason       TEXT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT runs_resume_status_check
        CHECK (resume_status IN ('UNKNOWN', 'AVAILABLE', 'UNAVAILABLE'))
);

CREATE INDEX runs_session_idx ON runs (session_id, created_at);
CREATE INDEX runs_backend_instance_idx ON runs (backend_instance_id);

CREATE TABLE jobs (
    id              UUID        PRIMARY KEY,
    run_id          UUID        NOT NULL REFERENCES runs (id) ON DELETE CASCADE,
    status          TEXT        NOT NULL DEFAULT 'QUEUED',
    -- Client request id making enqueue retries safe (section 27).
    idempotency_key TEXT,
    error           TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at      TIMESTAMPTZ,
    ended_at        TIMESTAMPTZ,

    CONSTRAINT jobs_status_check CHECK (status IN (
        'QUEUED', 'RUNNING', 'WAITING_INPUT', 'WAITING_VALIDATION',
        'WAITING_BACKEND', 'CANCELLING', 'COMPLETED', 'FAILED', 'CANCELLED'
    ))
);

-- A Run has at most one active Job and may have several queued ones
-- (section 3.4), enforced here rather than left to application discipline.
CREATE UNIQUE INDEX jobs_run_single_active_key
    ON jobs (run_id)
    WHERE status IN ('RUNNING', 'WAITING_INPUT', 'WAITING_VALIDATION',
                     'WAITING_BACKEND', 'CANCELLING');

CREATE UNIQUE INDEX jobs_idempotency_key
    ON jobs (idempotency_key)
    WHERE idempotency_key IS NOT NULL;

-- Queued Jobs are FIFO by default.
CREATE INDEX jobs_run_queue_idx ON jobs (run_id, created_at) WHERE status = 'QUEUED';

-- Persistent observable events. This is the timeline and the realtime
-- synchronization mechanism, not an event-sourcing journal.
CREATE TABLE events (
    id              UUID        PRIMARY KEY,
    -- Core-assigned monotonically increasing global sequence: the cursor of the
    -- user global SSE stream (section 4).
    global_sequence BIGINT      GENERATED ALWAYS AS IDENTITY,
    type            TEXT        NOT NULL,

    project_id UUID REFERENCES projects (id) ON DELETE CASCADE,
    session_id UUID REFERENCES sessions (id) ON DELETE CASCADE,
    run_id     UUID REFERENCES runs (id) ON DELETE CASCADE,
    job_id     UUID REFERENCES jobs (id) ON DELETE CASCADE,

    -- Backend event identity. backend_event_id deduplicates at-least-once
    -- backend delivery, backend_sequence orders and detects replay gaps within a
    -- Job. Both are NULL for Core-generated events.
    backend_instance_id UUID REFERENCES backend_instances (id) ON DELETE SET NULL,
    backend_event_id    TEXT,
    backend_sequence    BIGINT,

    payload     JSONB       NOT NULL DEFAULT '{}'::jsonb,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT events_global_sequence_key UNIQUE (global_sequence),
    -- Deduplication of at-least-once backend delivery. NULLs are distinct in
    -- PostgreSQL, so Core-generated events are unaffected.
    CONSTRAINT events_backend_event_key UNIQUE (backend_instance_id, backend_event_id),
    CONSTRAINT events_type_not_empty CHECK (length(btrim(type)) > 0)
);

CREATE INDEX events_session_sequence_idx ON events (session_id, global_sequence);
CREATE INDEX events_project_sequence_idx ON events (project_id, global_sequence);
CREATE INDEX events_job_backend_sequence_idx ON events (job_id, backend_sequence);
