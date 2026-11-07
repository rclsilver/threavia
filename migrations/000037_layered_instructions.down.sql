-- The supervision goes back into the policy of its level where there is one.
-- A level without a policy cannot take it back without gaining one, which
-- would replace the broader policies wholesale, so its text is lost. So are the
-- agent instructions of the global and backend levels, and the Session and Job
-- copies removed on the way up (the audit keeps them).
UPDATE instance_execution_policies i
SET execution_policy = jsonb_set(i.execution_policy, '{supervision}', to_jsonb(ii.supervision))
FROM instance_instructions ii
WHERE ii.owner_id = i.owner_id AND ii.supervision <> '' AND jsonb_typeof(i.execution_policy) = 'object';

UPDATE backend_instances
SET execution_policy = jsonb_set(execution_policy, '{supervision}', to_jsonb(supervision_instructions))
WHERE supervision_instructions <> '' AND jsonb_typeof(execution_policy) = 'object';

UPDATE projects
SET execution_policy = jsonb_set(execution_policy, '{supervision}', to_jsonb(supervision_instructions))
WHERE supervision_instructions <> '' AND jsonb_typeof(execution_policy) = 'object';

ALTER TABLE backend_instances DROP COLUMN supervision_instructions, DROP COLUMN agent_instructions;
ALTER TABLE projects DROP COLUMN supervision_instructions;
DROP TABLE instance_instructions;
