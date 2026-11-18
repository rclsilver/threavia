-- Notifications an agent raises for the person (decided 2026-11-18): INFO to
-- report, WARNING for what needs their attention. They belong to a Session and
-- stay listed until the person dismisses them or joins the conversation. The
-- Project is read through the Session, so a Session moved to another Project
-- takes its notifications along.
--
-- An identical unread notification of the same Session is not recorded twice:
-- a recurring schedule that finds the same thing bumps count instead.
CREATE TABLE notifications (
    id         UUID NOT NULL PRIMARY KEY,
    session_id UUID NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    run_id     UUID REFERENCES runs (id) ON DELETE SET NULL,
    job_id     UUID REFERENCES jobs (id) ON DELETE SET NULL,

    level   TEXT NOT NULL,
    title   TEXT NOT NULL,
    message TEXT NOT NULL,
    count   INTEGER NOT NULL DEFAULT 1,

    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    read_at    TIMESTAMPTZ,

    CONSTRAINT notifications_level_check CHECK (level IN ('INFO', 'WARNING'))
);

CREATE INDEX notifications_session_idx ON notifications (session_id, updated_at DESC);
CREATE UNIQUE INDEX notifications_unread_identical_idx
    ON notifications (session_id, level, (md5(title || E'\n' || message)))
    WHERE read_at IS NULL;
