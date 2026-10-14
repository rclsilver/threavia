-- Keep global metadata: restoring the project-only schema requires an explicit
-- reassignment first, rather than silently deleting directories and bindings.
ALTER TABLE known_directories ALTER COLUMN project_id SET NOT NULL;
DROP INDEX known_directories_owner_idx;
DROP INDEX known_directories_global_name_key;
ALTER TABLE known_directories DROP COLUMN owner_id;
