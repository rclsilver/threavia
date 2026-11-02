-- Quiet schedules (decided 2026-11-02): a recurring check that speaks only
-- when it has something to say. A run of a quiet Schedule makes a quiet Job,
-- frozen at creation so a later edit of the Schedule does not change what the
-- run may do. Its agent may dismiss it, and a dismissed Job ends without a
-- notification, without marking the Session unread and without moving it up
-- the list.
ALTER TABLE schedules ADD COLUMN quiet BOOLEAN NOT NULL DEFAULT false;

ALTER TABLE jobs
  ADD COLUMN quiet BOOLEAN NOT NULL DEFAULT false,
  ADD COLUMN dismissed BOOLEAN NOT NULL DEFAULT false,
  ADD CONSTRAINT jobs_dismissed_quiet CHECK (quiet OR NOT dismissed);
