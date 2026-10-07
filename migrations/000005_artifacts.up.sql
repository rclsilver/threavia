-- Artifacts (THREAVIA_SPEC_V1.md sections 19 and 24): metadata in PostgreSQL,
-- bytes in S3-compatible object storage.
--
-- Events and messages reference an artifact by id rather than embedding a blob,
-- which is what keeps the timeline something a client can page through.
CREATE TABLE artifacts (
    id         UUID        PRIMARY KEY,
    owner_id   TEXT        NOT NULL,
    project_id UUID        NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    -- The filename is metadata and nothing more: the object key is derived from
    -- identifiers, never from what a caller named the file (section 24).
    filename   TEXT        NOT NULL,
    mime_type  TEXT        NOT NULL DEFAULT 'application/octet-stream',
    size       BIGINT      NOT NULL,
    sha256     TEXT        NOT NULL,
    object_key TEXT        NOT NULL,
    -- Where it came from, when it came from somewhere in particular.
    session_id UUID REFERENCES sessions (id) ON DELETE SET NULL,
    job_id     UUID REFERENCES jobs (id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT artifacts_size_check CHECK (size >= 0),
    CONSTRAINT artifacts_sha256_check CHECK (length(sha256) = 64),
    CONSTRAINT artifacts_filename_not_empty CHECK (length(btrim(filename)) > 0)
);

CREATE UNIQUE INDEX artifacts_object_key_key ON artifacts (object_key);
CREATE INDEX artifacts_project_idx ON artifacts (project_id, created_at DESC);
CREATE INDEX artifacts_sha256_idx ON artifacts (project_id, sha256);
