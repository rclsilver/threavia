-- Agent and supervision instructions written per level: global (per owner),
-- project and backend. Each level adds to the ones above it, and Core
-- assembles them per Job.
--
-- Supervision used to be a field of the execution policy, and a policy is
-- replaced wholesale by a narrower one: a Project that set its own policy only
-- to change the mode silently dropped the global supervision. It now leaves the
-- instance, backend and project policies for columns of its own. Session and
-- Job policies keep theirs, as the two narrowest layers.

CREATE TABLE instance_instructions (
    owner_id    TEXT        PRIMARY KEY,
    agent       TEXT        NOT NULL DEFAULT '',
    supervision TEXT        NOT NULL DEFAULT '',
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- projects.instructions already holds the agent text of a Project.
ALTER TABLE projects ADD COLUMN supervision_instructions TEXT NOT NULL DEFAULT '';
-- A backend's instructions are its owner's, like its execution policy.
ALTER TABLE backend_instances
    ADD COLUMN agent_instructions       TEXT NOT NULL DEFAULT '',
    ADD COLUMN supervision_instructions TEXT NOT NULL DEFAULT '';

-- Session and Job policies often hold a copy of the supervision they
-- inherited: "Override" pre-fills the form with the effective policy, and the
-- delegation and the monitors store an effective policy as it is. Once the
-- levels add up, such a copy would reach the reviewer twice. A copy is
-- recognised as a text equal, after trimming, to what the old rule gave the
-- level: the supervision of the narrowest non-null policy above it, taken
-- whole. It is removed, which loses nothing since the text stays at the level
-- it came from, and each removal is audited. A different text is the user's
-- own and is kept.
--
-- Jobs go first: their inherited value includes the Session policy, and must
-- be read before the Session's own copy is taken out. A Job is pinned to its
-- Run, so its Run's backend is the one it inherited from.
--
-- A Session is not pinned: Core took the backend of its latest Run, and the
-- next Run may use another. Its own text applied whatever the backend, while
-- the backend's applies only on that backend, so a copy matched against the
-- backend is removed only when every Run of the Session used that backend.
-- One matched against the project or the instance reaches it either way.
--
-- btrim covers ASCII whitespace only. A copy that differs from its source by
-- Unicode whitespace is kept, which duplicates the text rather than losing it.
WITH copies AS (
    SELECT j.id AS job_id, sess.id AS session_id, p.id AS project_id, p.owner_id,
           j.execution_policy->>'supervision' AS supervision
    FROM jobs j
    JOIN runs r ON r.id = j.run_id
    JOIN sessions sess ON sess.id = r.session_id
    JOIN projects p ON p.id = sess.project_id
    LEFT JOIN backend_instances b ON b.id = r.backend_instance_id
    LEFT JOIN instance_execution_policies i ON i.owner_id = p.owner_id
    WHERE btrim(COALESCE(j.execution_policy->>'supervision', ''), E' \t\n\r\f\v') <> ''
      AND btrim(j.execution_policy->>'supervision', E' \t\n\r\f\v') =
          btrim(COALESCE(COALESCE(sess.execution_policy, p.execution_policy, b.execution_policy, i.execution_policy)->>'supervision', ''), E' \t\n\r\f\v')
), audited AS (
    INSERT INTO audit_entries (id, owner_id, actor_id, action, project_id, session_id, job_id, detail)
    SELECT gen_random_uuid(), owner_id, 'migration', 'instructions.migration_copy_removed',
           project_id, session_id, job_id, jsonb_build_object('supervision', supervision)
    FROM copies
)
UPDATE jobs SET execution_policy = jobs.execution_policy - 'supervision'
FROM copies WHERE jobs.id = copies.job_id;

WITH copies AS (
    SELECT s.id AS session_id, p.id AS project_id, p.owner_id,
           s.execution_policy->>'supervision' AS supervision
    FROM sessions s
    JOIN projects p ON p.id = s.project_id
    LEFT JOIN LATERAL (
        SELECT backend_instance_id FROM runs WHERE session_id = s.id
        ORDER BY created_at DESC, id DESC LIMIT 1
    ) r ON true
    LEFT JOIN LATERAL (
        SELECT count(DISTINCT backend_instance_id) AS backends FROM runs WHERE session_id = s.id
    ) used ON true
    LEFT JOIN backend_instances b ON b.id = r.backend_instance_id
    LEFT JOIN instance_execution_policies i ON i.owner_id = p.owner_id
    WHERE btrim(COALESCE(s.execution_policy->>'supervision', ''), E' \t\n\r\f\v') <> ''
      AND btrim(s.execution_policy->>'supervision', E' \t\n\r\f\v') =
          btrim(COALESCE(COALESCE(p.execution_policy, b.execution_policy, i.execution_policy)->>'supervision', ''), E' \t\n\r\f\v')
      -- The match came from the project or the instance, or from a backend the
      -- Session never left.
      AND (p.execution_policy IS NOT NULL OR b.execution_policy IS NULL OR used.backends = 1)
), audited AS (
    INSERT INTO audit_entries (id, owner_id, actor_id, action, project_id, session_id, detail)
    SELECT gen_random_uuid(), owner_id, 'migration', 'instructions.migration_copy_removed',
           project_id, session_id, jsonb_build_object('supervision', supervision)
    FROM copies
)
UPDATE sessions SET execution_policy = sessions.execution_policy - 'supervision'
FROM copies WHERE sessions.id = copies.session_id;

-- Backend and project policies were pre-filled the same way, from the
-- instance policy, so many hold a copy of the instance supervision. These go
-- after the Sessions and Jobs, whose copies are judged against these policies
-- as they were. A project is only matched against the instance, never a
-- backend: it is not pinned to one.
WITH copies AS (
    SELECT b.id AS backend_id, b.owner_id, b.execution_policy->>'supervision' AS supervision
    FROM backend_instances b
    JOIN instance_execution_policies i ON i.owner_id = b.owner_id
    WHERE btrim(COALESCE(b.execution_policy->>'supervision', ''), E' \t\n\r\f\v') <> ''
      AND btrim(b.execution_policy->>'supervision', E' \t\n\r\f\v') =
          btrim(COALESCE(i.execution_policy->>'supervision', ''), E' \t\n\r\f\v')
), audited AS (
    INSERT INTO audit_entries (id, owner_id, actor_id, action, subject_id, detail)
    SELECT gen_random_uuid(), owner_id, 'migration', 'instructions.migration_copy_removed',
           backend_id, jsonb_build_object('supervision', supervision)
    FROM copies
)
UPDATE backend_instances SET execution_policy = backend_instances.execution_policy - 'supervision'
FROM copies WHERE backend_instances.id = copies.backend_id;

WITH copies AS (
    SELECT p.id AS project_id, p.owner_id, p.execution_policy->>'supervision' AS supervision
    FROM projects p
    JOIN instance_execution_policies i ON i.owner_id = p.owner_id
    WHERE btrim(COALESCE(p.execution_policy->>'supervision', ''), E' \t\n\r\f\v') <> ''
      AND btrim(p.execution_policy->>'supervision', E' \t\n\r\f\v') =
          btrim(COALESCE(i.execution_policy->>'supervision', ''), E' \t\n\r\f\v')
), audited AS (
    INSERT INTO audit_entries (id, owner_id, actor_id, action, project_id, subject_id, detail)
    SELECT gen_random_uuid(), owner_id, 'migration', 'instructions.migration_copy_removed',
           project_id, project_id, jsonb_build_object('supervision', supervision)
    FROM copies
)
UPDATE projects SET execution_policy = projects.execution_policy - 'supervision'
FROM copies WHERE projects.id = copies.project_id;

-- Move the supervision of the three broad levels to their new place. A blank
-- text carries nothing and is dropped without an audit row.
INSERT INTO instance_instructions (owner_id, supervision)
SELECT owner_id, execution_policy->>'supervision'
FROM instance_execution_policies
WHERE btrim(COALESCE(execution_policy->>'supervision', ''), E' \t\n\r\f\v') <> '';
UPDATE instance_execution_policies SET execution_policy = execution_policy - 'supervision'
WHERE jsonb_typeof(execution_policy) = 'object' AND execution_policy ? 'supervision';

UPDATE backend_instances SET supervision_instructions = execution_policy->>'supervision'
WHERE btrim(COALESCE(execution_policy->>'supervision', ''), E' \t\n\r\f\v') <> '';
UPDATE backend_instances SET execution_policy = execution_policy - 'supervision'
WHERE jsonb_typeof(execution_policy) = 'object' AND execution_policy ? 'supervision';

UPDATE projects SET supervision_instructions = execution_policy->>'supervision'
WHERE btrim(COALESCE(execution_policy->>'supervision', ''), E' \t\n\r\f\v') <> '';
UPDATE projects SET execution_policy = execution_policy - 'supervision'
WHERE jsonb_typeof(execution_policy) = 'object' AND execution_policy ? 'supervision';
