-- Defaults are owned by the authenticated user, like projects and backends.
-- One account cannot change another account's execution permissions.
CREATE TABLE instance_execution_policies (
    owner_id TEXT PRIMARY KEY,
    execution_policy JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER TABLE backend_instances ADD COLUMN execution_policy JSONB;
