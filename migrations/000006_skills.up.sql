-- Skills and instructions (THREAVIA_SPEC_V1.md section 18).
--
-- Core-managed Project Skills are immutable, versioned artefacts with
-- provenance: what was asked for, and what was actually installed. Backend-local
-- Skills are known by metadata only; Core never holds their content, which is
-- what lets a backend expose a Skill that only exists behind a corporate
-- network.

-- Provider-independent project rules. Provider files (CLAUDE.md, AGENTS.md) are
-- a backend mapping concern and are never modelled here.
ALTER TABLE projects ADD COLUMN instructions TEXT NOT NULL DEFAULT '';

CREATE TABLE skills (
    id          UUID PRIMARY KEY,
    owner_id    TEXT NOT NULL,
    project_id  UUID NOT NULL REFERENCES projects (id) ON DELETE CASCADE,

    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',

    -- Provenance: what was requested.
    source_type     TEXT NOT NULL,
    source_url      TEXT NOT NULL DEFAULT '',
    source_path     TEXT NOT NULL DEFAULT '',
    source_revision TEXT NOT NULL DEFAULT '',

    -- What was actually installed: an immutable commit or content hash. A Run
    -- always says which one it used.
    installed_revision TEXT NOT NULL,
    installed_at       TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- The packed bundle. ON DELETE RESTRICT: removing the bytes of a Skill that
    -- is still installed would leave a Project pointing at nothing.
    artifact_id UUID NOT NULL REFERENCES artifacts (id) ON DELETE RESTRICT,
    -- Checksum of the bundle, so a backend can tell a cached copy from a stale
    -- one without asking.
    bundle_sha256 TEXT NOT NULL,

    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT skills_source_type_known
        CHECK (source_type IN ('GIT', 'ARCHIVE', 'UPLOAD')),
    CONSTRAINT skills_bundle_sha256_shape
        CHECK (char_length(bundle_sha256) = 64)
);

-- One Skill name per Project: the name is how an agent refers to it.
CREATE UNIQUE INDEX skills_project_name ON skills (project_id, name);
CREATE INDEX skills_project ON skills (project_id);

-- Backend-local Skills, reported by the backend as metadata only (section 18).
-- Content is never fetched, which is the point: a handoff can report that a
-- local Skill is unavailable on another backend without Core ever holding it.
CREATE TABLE backend_skills (
    backend_instance_id UUID NOT NULL REFERENCES backend_instances (id) ON DELETE CASCADE,
    name                TEXT NOT NULL,
    description         TEXT NOT NULL DEFAULT '',
    available           BOOLEAN NOT NULL DEFAULT TRUE,
    reported_at         TIMESTAMPTZ NOT NULL DEFAULT now(),

    PRIMARY KEY (backend_instance_id, name)
);
