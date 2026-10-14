ALTER TABLE backend_instances ADD COLUMN is_default BOOLEAN NOT NULL DEFAULT FALSE;
CREATE UNIQUE INDEX backend_instances_owner_default_key ON backend_instances (owner_id) WHERE is_default;
ALTER TABLE backend_instances ADD CONSTRAINT backend_instances_default_claimed CHECK (NOT is_default OR (owner_id IS NOT NULL AND ownership_status = 'CLAIMED'));
