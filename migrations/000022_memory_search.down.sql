ALTER TABLE decisions DROP COLUMN search;
ALTER TABLE decisions ADD COLUMN search TSVECTOR GENERATED ALWAYS AS (
    to_tsvector('simple', title || ' ' || content)
) STORED;
CREATE INDEX decisions_search_idx ON decisions USING GIN (search);

ALTER TABLE events DROP COLUMN search;
ALTER TABLE events ADD COLUMN search TSVECTOR GENERATED ALWAYS AS (
    to_tsvector('simple', coalesce(payload ->> 'text', '') || ' ' || coalesce(payload ->> 'summary', ''))
) STORED;
CREATE INDEX events_search_idx ON events USING GIN (search);

DROP FUNCTION memory_search_coverage(TSVECTOR, TEXT[]);
DROP FUNCTION memory_search_query(TEXT);
DROP FUNCTION memory_search_terms(TEXT);
DROP FUNCTION memory_search_vector(TEXT);
