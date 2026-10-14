-- Global memory belongs to the user, independently of any project's lifetime.
ALTER TABLE decisions ADD COLUMN owner_id TEXT;
UPDATE decisions d SET owner_id = p.owner_id FROM projects p WHERE p.id = d.project_id;
ALTER TABLE decisions ALTER COLUMN owner_id SET NOT NULL;
ALTER TABLE decisions ALTER COLUMN project_id DROP NOT NULL;
CREATE INDEX decisions_global_idx ON decisions (owner_id, status, importance, created_at DESC) WHERE project_id IS NULL;
