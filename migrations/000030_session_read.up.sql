-- How far the person has read a Session: the global sequence of the last event
-- a client showed them. A Session is unread while an agent message or a failed
-- Job sits past it. Every existing Session starts read, so the feature does not
-- open on a sidebar full of conversations nobody missed.
ALTER TABLE sessions ADD COLUMN read_sequence BIGINT NOT NULL DEFAULT 0;

UPDATE sessions s
SET read_sequence = coalesce((SELECT max(e.global_sequence) FROM events e WHERE e.session_id = s.id), 0);
