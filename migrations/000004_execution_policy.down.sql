ALTER TABLE jobs DROP COLUMN IF EXISTS origin_channel;
DROP TABLE IF EXISTS audit_entries;
ALTER TABLE jobs DROP COLUMN IF EXISTS execution_policy;
ALTER TABLE sessions DROP COLUMN IF EXISTS execution_policy;
