-- The session whose agent sent, with session_send, the message that made a
-- Job (decided 2026-11-07). A reply from that Job to it calls nobody back.
-- The callback session, usually the same, is told of the Job's questions,
-- permissions and end, and may answer them, as a manager would for its
-- delegates. Only a Job the message created carries them; a message reaching
-- a running Job does not take it over.
ALTER TABLE jobs
 ADD COLUMN sender_session_id UUID REFERENCES sessions(id) ON DELETE SET NULL,
 ADD COLUMN callback_session_id UUID REFERENCES sessions(id) ON DELETE SET NULL;
CREATE INDEX jobs_callback_session_idx ON jobs(callback_session_id) WHERE callback_session_id IS NOT NULL;
