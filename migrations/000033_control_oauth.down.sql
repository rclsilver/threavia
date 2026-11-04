DROP TABLE IF EXISTS control_oauth_tokens;
DROP TABLE IF EXISTS control_oauth_codes;
-- OAuth grants have no static secret and cannot survive without their tokens.
DELETE FROM control_accesses WHERE secret_hash IS NULL;
ALTER TABLE control_accesses
 DROP COLUMN IF EXISTS oauth_client_id,
 ALTER COLUMN secret_hash SET NOT NULL;
DROP TABLE IF EXISTS control_oauth_clients;
