-- Two people may have used the same key since the up migration; the global
-- index cannot be restored over them, so the later Jobs lose their key rather
-- than the rollback failing.
UPDATE jobs j
SET idempotency_key = NULL
WHERE j.idempotency_key IS NOT NULL
  AND EXISTS (
      SELECT 1 FROM jobs earlier
      WHERE earlier.idempotency_key = j.idempotency_key
        AND (earlier.created_at, earlier.id) < (j.created_at, j.id)
  );

DROP INDEX jobs_idempotency_key;

CREATE UNIQUE INDEX jobs_idempotency_key
    ON jobs (idempotency_key)
    WHERE idempotency_key IS NOT NULL;

ALTER TABLE jobs DROP COLUMN IF EXISTS idempotency_owner_id;
