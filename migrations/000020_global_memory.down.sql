-- Refuse to discard shared memory as a side effect of a schema rollback.
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM decisions WHERE project_id IS NULL) THEN
        RAISE EXCEPTION 'Global decisions must be exported or removed before rolling back global memory';
    END IF;
END $$;
DROP INDEX decisions_global_idx;
ALTER TABLE decisions ALTER COLUMN project_id SET NOT NULL;
ALTER TABLE decisions DROP COLUMN owner_id;
