DROP INDEX IF EXISTS sessions_pinned_idx;
ALTER TABLE sessions DROP COLUMN IF EXISTS pinned_at;
