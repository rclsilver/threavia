ALTER TABLE sessions ADD COLUMN manager_session_id UUID REFERENCES sessions(id) ON DELETE SET NULL;
ALTER TABLE sessions ADD CONSTRAINT sessions_manager_not_self CHECK (manager_session_id IS NULL OR manager_session_id <> id);
CREATE INDEX sessions_manager_idx ON sessions(manager_session_id) WHERE manager_session_id IS NOT NULL;
