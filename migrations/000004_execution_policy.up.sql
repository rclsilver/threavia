-- ExecutionPolicy (THREAVIA_SPEC_V1.md section 17): a Session default with an
-- optional per-Job override.
--
-- It is stored as JSON because section 17 calls it "simple and extensible": the
-- shape will gain limits, and a column per limit would mean a migration per
-- idea. What matters is that it is enforced, which happens in Core and in the
-- backend permission gate, never by asking the model nicely.

ALTER TABLE sessions ADD COLUMN execution_policy JSONB;
ALTER TABLE jobs ADD COLUMN execution_policy JSONB;

-- The audit trail of section 23. A validation receipt is already an event, but
-- an event is part of a timeline a project deletion takes with it; an audit
-- entry outlives its subject deliberately.
CREATE TABLE audit_entries (
    id         UUID        PRIMARY KEY,
    owner_id   TEXT        NOT NULL,
    actor_id   TEXT        NOT NULL,
    action     TEXT        NOT NULL,
    -- Subject identifiers are plain text, not foreign keys: the record must
    -- survive the deletion of what it describes.
    project_id TEXT,
    session_id TEXT,
    job_id     TEXT,
    subject_id TEXT,
    channel    TEXT        NOT NULL DEFAULT '',
    -- SHA-256 of the canonical payload the actor decided on.
    payload_sha256 TEXT    NOT NULL DEFAULT '',
    detail     JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT audit_entries_action_not_empty CHECK (length(btrim(action)) > 0)
);

CREATE INDEX audit_entries_owner_idx ON audit_entries (owner_id, created_at DESC);
CREATE INDEX audit_entries_subject_idx ON audit_entries (subject_id);

-- Which client channel started a Job, so a notification can tell work the user
-- is watching from work they walked away from (section 6).
ALTER TABLE jobs ADD COLUMN origin_channel TEXT NOT NULL DEFAULT '';
