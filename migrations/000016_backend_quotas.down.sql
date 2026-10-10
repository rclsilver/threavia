ALTER TABLE backend_instances DROP COLUMN quotas;
DROP INDEX events_owner_cursor_idx;
ALTER TABLE events DROP COLUMN owner_id;
