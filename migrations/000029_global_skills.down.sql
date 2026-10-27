-- Keep global Skills: restoring the project-only schema requires an explicit
-- reassignment first, rather than silently deleting Skills and their bundles.
DROP TABLE skill_project_exclusions;
ALTER TABLE artifacts ALTER COLUMN project_id SET NOT NULL;
DROP INDEX skills_global_owner;
DROP INDEX skills_global_name;
ALTER TABLE skills ALTER COLUMN project_id SET NOT NULL;
