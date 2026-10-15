ALTER TABLE jobs ADD COLUMN backend_wait jsonb;
ALTER TABLE jobs ADD COLUMN provider_active_seconds double precision NOT NULL DEFAULT 0;
ALTER TABLE jobs ADD COLUMN provider_attempt_started_at timestamptz;
