-- The optional CODE features a backend announces in its Hello (spec sections
-- 3.5 and 7): today, whether a message can reach a Job while it runs, after an
-- interruption (JOB_INPUT_NOW) or at its next step (JOB_INPUT_NEXT).
--
-- Kept with the instance rather than read from the live connection, so a
-- client can offer only what the backend holding a Session knows how to do,
-- and say so even while that backend is briefly away.
ALTER TABLE backend_instances
    ADD COLUMN features TEXT[] NOT NULL DEFAULT '{}';
