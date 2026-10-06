DROP TABLE IF EXISTS backend_registration_tokens;

DROP INDEX IF EXISTS backend_instances_credential_key;
ALTER TABLE backend_instances
    DROP COLUMN IF EXISTS credential_sha256,
    DROP COLUMN IF EXISTS claim_code_sha256,
    DROP COLUMN IF EXISTS claim_code_expires_at;

DROP TABLE IF EXISTS user_input_requests;
DROP TABLE IF EXISTS validation_requests;

ALTER TABLE sessions DROP CONSTRAINT IF EXISTS sessions_working_directory_fkey;

DROP TABLE IF EXISTS known_directory_bindings;
DROP TABLE IF EXISTS known_directories;
