ALTER TABLE sessions ADD COLUMN model text NOT NULL DEFAULT '', ADD COLUMN effort text NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN model text NOT NULL DEFAULT '', ADD COLUMN effort text NOT NULL DEFAULT '';
ALTER TABLE backend_instances ADD COLUMN model_catalog jsonb;
