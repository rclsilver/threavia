-- A Session can be pinned, to be one click away from any Project. The time it
-- was pinned is kept rather than a flag, so pinned Sessions keep the order in
-- which the person pinned them.
ALTER TABLE sessions ADD COLUMN pinned_at TIMESTAMPTZ;

CREATE INDEX sessions_pinned_idx ON sessions (pinned_at) WHERE pinned_at IS NOT NULL;
