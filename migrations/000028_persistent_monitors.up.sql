CREATE TABLE monitors (
 id UUID PRIMARY KEY,
 owner_id TEXT NOT NULL,
 session_id UUID NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
 backend_id UUID NOT NULL REFERENCES backend_instances(id) ON DELETE CASCADE,
 snapshot JSONB NOT NULL,
 policy JSONB NOT NULL,
 last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 stop_requested BOOLEAN NOT NULL DEFAULT false,
 wake_sequence BIGINT NOT NULL DEFAULT 0,
 wake_count INTEGER NOT NULL DEFAULT 0,
 wake_job_id UUID REFERENCES jobs(id) ON DELETE SET NULL
);
CREATE INDEX monitors_session ON monitors(session_id);
