-- A directory without a project is shared by all projects of its owner.
ALTER TABLE known_directories ADD COLUMN owner_id TEXT;
UPDATE known_directories d SET owner_id = p.owner_id FROM projects p WHERE p.id = d.project_id;
ALTER TABLE known_directories ALTER COLUMN owner_id SET NOT NULL;
ALTER TABLE known_directories ALTER COLUMN project_id DROP NOT NULL;
CREATE UNIQUE INDEX known_directories_global_name_key ON known_directories (owner_id, name) WHERE project_id IS NULL;
CREATE INDEX known_directories_owner_idx ON known_directories (owner_id);
