-- Messages sent to a Session on a schedule (decided 2026-10-08): "every
-- morning at 8, check X" becomes a Job like any message, at the time a cron
-- expression names, in the time zone it was written in.
--
-- next_run_at is what the scheduler claims: a run is taken by moving it
-- forward from the value read, so two Core replicas never send the same one.
-- A run that could not happen — Core was down, the backend was away, the
-- previous Job was still running — is skipped and said in the Session, never
-- replayed later as if on time.
CREATE TABLE schedules (
    id           UUID PRIMARY KEY,
    session_id   UUID NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    cron         TEXT NOT NULL,
    timezone     TEXT NOT NULL,
    message      TEXT NOT NULL,
    enabled      BOOLEAN NOT NULL DEFAULT TRUE,
    next_run_at  TIMESTAMPTZ,
    last_run_at  TIMESTAMPTZ,
    last_outcome TEXT NOT NULL DEFAULT '',
    last_job_id  UUID REFERENCES jobs (id) ON DELETE SET NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT schedules_message_not_empty CHECK (length(btrim(message)) > 0)
);

CREATE INDEX schedules_due ON schedules (next_run_at) WHERE enabled;
CREATE INDEX schedules_session ON schedules (session_id);
