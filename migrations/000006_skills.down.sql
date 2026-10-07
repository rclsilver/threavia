DROP TABLE IF EXISTS backend_skills;
DROP INDEX IF EXISTS skills_project;
DROP INDEX IF EXISTS skills_project_name;
DROP TABLE IF EXISTS skills;
ALTER TABLE projects DROP COLUMN IF EXISTS instructions;
