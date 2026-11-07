-- Deleting through control MCP is its own right, never implied by manage.
ALTER TABLE control_accesses DROP CONSTRAINT control_accesses_scopes_check;
ALTER TABLE control_accesses ADD CONSTRAINT control_accesses_scopes_check
  CHECK (cardinality(scopes) > 0 AND scopes <@ ARRAY['read','manage','delete','answer','approve']::text[]);
ALTER TABLE control_oauth_codes DROP CONSTRAINT control_oauth_codes_scopes_check;
ALTER TABLE control_oauth_codes ADD CONSTRAINT control_oauth_codes_scopes_check
  CHECK (cardinality(scopes) > 0 AND scopes <@ ARRAY['read','manage','delete','answer','approve']::text[]);
