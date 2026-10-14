ALTER TABLE backend_instances DROP COLUMN model_catalog;
ALTER TABLE jobs DROP COLUMN model, DROP COLUMN effort;
ALTER TABLE sessions DROP COLUMN model, DROP COLUMN effort;
