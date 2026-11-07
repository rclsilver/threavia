-- Dropping the right must not widen anything: an access left with no other
-- right is revoked rather than given one.
UPDATE control_accesses SET revoked_at = COALESCE(revoked_at, clock_timestamp()), scopes = ARRAY['read']::text[]
  WHERE scopes = ARRAY['delete']::text[];
UPDATE control_accesses SET scopes = array_remove(scopes, 'delete') WHERE 'delete' = ANY(scopes);
DELETE FROM control_oauth_codes WHERE 'delete' = ANY(scopes);
ALTER TABLE control_accesses DROP CONSTRAINT control_accesses_scopes_check;
ALTER TABLE control_accesses ADD CONSTRAINT control_accesses_scopes_check
  CHECK (cardinality(scopes) > 0 AND scopes <@ ARRAY['read','manage','answer','approve']::text[]);
ALTER TABLE control_oauth_codes DROP CONSTRAINT control_oauth_codes_scopes_check;
ALTER TABLE control_oauth_codes ADD CONSTRAINT control_oauth_codes_scopes_check
  CHECK (cardinality(scopes) > 0 AND scopes <@ ARRAY['read','manage','answer','approve']::text[]);
