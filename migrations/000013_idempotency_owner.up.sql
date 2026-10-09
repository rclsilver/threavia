-- An Idempotency-Key belongs to the person who sent it (security review,
-- 2026-10-09). The key used to be unique across every user: whoever knew
-- another user's key could read the Job it had made, or take the key first so
-- that the other user's message was silently answered with someone else's Job.
--
-- idempotency_owner_id is the owner of the Project the Job was made in, which
-- is the person whose request carried the key. It is recorded on the Job rather
-- than joined at lookup time so that the uniqueness itself is per person, and a
-- key without an owner is refused: NULLs are distinct in a unique index, so such
-- a key would escape it.
ALTER TABLE jobs ADD COLUMN idempotency_owner_id TEXT;

UPDATE jobs j
SET idempotency_owner_id = p.owner_id
FROM runs r
JOIN sessions s ON s.id = r.session_id
JOIN projects p ON p.id = s.project_id
WHERE r.id = j.run_id AND j.idempotency_key IS NOT NULL;

ALTER TABLE jobs ADD CONSTRAINT jobs_idempotency_key_has_owner
    CHECK (idempotency_key IS NULL OR idempotency_owner_id IS NOT NULL);

DROP INDEX jobs_idempotency_key;

CREATE UNIQUE INDEX jobs_idempotency_key
    ON jobs (idempotency_owner_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;
