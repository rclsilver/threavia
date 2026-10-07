-- Project knowledge: the explicit structured layer of THREAVIA_SPEC_V1.md
-- section 15, plus the full-text index behind its episodic layer.
--
-- Searchable text uses the 'simple' configuration rather than a language one:
-- a project's history mixes languages, and English stemming mangles French as
-- surely as the reverse.

-- A Decision is a durable project ruling. Active IMPORTANT ones are injected
-- into every new Run context; NORMAL ones are searchable on demand
-- (section 13).
CREATE TABLE decisions (
    id         UUID        PRIMARY KEY,
    project_id UUID        NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    title      TEXT        NOT NULL,
    content    TEXT        NOT NULL DEFAULT '',
    importance TEXT        NOT NULL DEFAULT 'NORMAL',
    status     TEXT        NOT NULL DEFAULT 'ACTIVE',
    -- A new Decision may supersede an older one, which then stays historical
    -- but is no longer current.
    supersedes UUID        REFERENCES decisions (id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    search TSVECTOR GENERATED ALWAYS AS (
        to_tsvector('simple', title || ' ' || content)
    ) STORED,

    CONSTRAINT decisions_importance_check CHECK (importance IN ('IMPORTANT', 'NORMAL')),
    CONSTRAINT decisions_status_check CHECK (status IN ('ACTIVE', 'SUPERSEDED')),
    CONSTRAINT decisions_title_not_empty CHECK (length(btrim(title)) > 0),
    CONSTRAINT decisions_no_self_supersede CHECK (supersedes IS NULL OR supersedes <> id)
);

CREATE INDEX decisions_project_idx ON decisions (project_id, status, importance, created_at DESC);
CREATE INDEX decisions_search_idx ON decisions USING GIN (search);

-- A Task. There is deliberately no BLOCKED status: blocked is derived from
-- incomplete dependencies, so it can never drift from the truth (section 14).
CREATE TABLE tasks (
    id           UUID        PRIMARY KEY,
    project_id   UUID        NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    title        TEXT        NOT NULL,
    description  TEXT        NOT NULL DEFAULT '',
    status       TEXT        NOT NULL DEFAULT 'TODO',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ,

    search TSVECTOR GENERATED ALWAYS AS (
        to_tsvector('simple', title || ' ' || description)
    ) STORED,

    CONSTRAINT tasks_status_check CHECK (status IN ('TODO', 'IN_PROGRESS', 'DONE')),
    CONSTRAINT tasks_title_not_empty CHECK (length(btrim(title)) > 0),
    CONSTRAINT tasks_completed_shape CHECK (
        (status = 'DONE' AND completed_at IS NOT NULL) OR
        (status <> 'DONE' AND completed_at IS NULL)
    )
);

CREATE INDEX tasks_project_idx ON tasks (project_id, status, created_at);
CREATE INDEX tasks_search_idx ON tasks USING GIN (search);

-- Dependencies are graph edges, not a hierarchy: a Task may depend on several
-- others. Cycles are rejected in the domain layer, where the whole graph can be
-- walked; the database rules out only the self-edge it can see on its own.
CREATE TABLE task_dependencies (
    task_id            UUID        NOT NULL REFERENCES tasks (id) ON DELETE CASCADE,
    depends_on_task_id UUID        NOT NULL REFERENCES tasks (id) ON DELETE CASCADE,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),

    PRIMARY KEY (task_id, depends_on_task_id),
    CONSTRAINT task_dependencies_no_self_edge CHECK (task_id <> depends_on_task_id)
);

CREATE INDEX task_dependencies_depends_on_idx ON task_dependencies (depends_on_task_id);

-- The optional many-to-many relation of section 14: which Jobs worked on which
-- Tasks.
CREATE TABLE job_tasks (
    job_id     UUID        NOT NULL REFERENCES jobs (id) ON DELETE CASCADE,
    task_id    UUID        NOT NULL REFERENCES tasks (id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    PRIMARY KEY (job_id, task_id)
);

CREATE INDEX job_tasks_task_idx ON job_tasks (task_id);

-- The episodic layer: messages and summaries become searchable without any
-- embedding or vector database (section 15).
ALTER TABLE events ADD COLUMN search TSVECTOR GENERATED ALWAYS AS (
    to_tsvector('simple',
        coalesce(payload ->> 'text', '') || ' ' || coalesce(payload ->> 'summary', ''))
) STORED;

CREATE INDEX events_search_idx ON events USING GIN (search);
