ALTER TABLE backend_instances ADD COLUMN quotas JSONB;
-- Owner-scoped notifications survive deletion of the object they describe.
ALTER TABLE events ADD COLUMN owner_id TEXT;
CREATE INDEX events_owner_cursor_idx ON events (owner_id, global_sequence) WHERE owner_id IS NOT NULL;
