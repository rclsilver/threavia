-- A Skill without a project is global: its owner's projects use it unless one
-- of them turns it off. A project Skill of the same name takes precedence.
ALTER TABLE skills ALTER COLUMN project_id DROP NOT NULL;
CREATE UNIQUE INDEX skills_global_name ON skills (owner_id, name) WHERE project_id IS NULL;
CREATE INDEX skills_global_owner ON skills (owner_id) WHERE project_id IS NULL;

-- The bundle of a global Skill belongs to its owner, not to a project, so
-- deleting a project never takes with it what other projects use.
ALTER TABLE artifacts ALTER COLUMN project_id DROP NOT NULL;

-- The projects in which a global Skill was turned off. Active is the default,
-- so a new project gets every global Skill.
CREATE TABLE skill_project_exclusions (
 skill_id UUID NOT NULL REFERENCES skills(id) ON DELETE CASCADE,
 project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
 PRIMARY KEY(skill_id, project_id)
);
