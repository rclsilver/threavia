-- Why a backend is in the state it reports (THREAVIA_SPEC_V1.md sections 7
-- and 23).
--
-- The operational status says a backend is DEGRADED; a condition says the
-- Claude Code executable is missing. Without this, the only place that answer
-- existed was a line in the Core log, where the person who has to fix it never
-- looks.
CREATE TABLE backend_conditions (
    backend_instance_id UUID NOT NULL REFERENCES backend_instances (id) ON DELETE CASCADE,
    type                TEXT NOT NULL,
    status              TEXT NOT NULL,
    reason              TEXT NOT NULL DEFAULT '',
    message             TEXT NOT NULL DEFAULT '',
    observed_at         TIMESTAMPTZ NOT NULL DEFAULT now(),

    PRIMARY KEY (backend_instance_id, type),
    CONSTRAINT backend_conditions_type_not_empty CHECK (length(btrim(type)) > 0)
);
