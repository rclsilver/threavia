-- Split technical punctuation consistently in documents and queries. Keep
-- underscores: environment variables such as SSH_AUTH_SOCK remain whole words.
-- 'simple' preserves vocabulary in the multilingual memory without stemming.
CREATE FUNCTION memory_search_vector(value TEXT) RETURNS TSVECTOR
LANGUAGE SQL IMMUTABLE STRICT PARALLEL SAFE AS $$
    SELECT to_tsvector('simple', regexp_replace(value, '[^[:alnum:]_]+', ' ', 'g'))
$$;

CREATE FUNCTION memory_search_terms(value TEXT) RETURNS TEXT[]
LANGUAGE SQL IMMUTABLE STRICT PARALLEL SAFE AS $$
    SELECT tsvector_to_array(memory_search_vector(value))
$$;

-- Quote each lexeme, never interpret prose as tsquery syntax. An empty or
-- punctuation-only query yields an empty query and no matches.
CREATE FUNCTION memory_search_query(value TEXT) RETURNS TSQUERY
LANGUAGE SQL IMMUTABLE STRICT PARALLEL SAFE AS $$
    SELECT coalesce(string_agg(quote_literal(term), ' | '), '')::tsquery
    FROM unnest(memory_search_terms(value)) AS term
$$;

-- Distinct term coverage comes before frequency: repeating one query word
-- cannot outrank a document that covers several query words.
CREATE FUNCTION memory_search_coverage(document TSVECTOR, terms TEXT[]) RETURNS INTEGER
LANGUAGE SQL IMMUTABLE STRICT PARALLEL SAFE AS $$
    SELECT count(*)::integer FROM (
        SELECT unnest(tsvector_to_array(document))
        INTERSECT SELECT unnest(terms)
    ) AS matches
$$;

ALTER TABLE decisions DROP COLUMN search;
ALTER TABLE decisions ADD COLUMN search TSVECTOR GENERATED ALWAYS AS (
    setweight(memory_search_vector(title), 'A') ||
    setweight(memory_search_vector(content), 'B')
) STORED;
CREATE INDEX decisions_search_idx ON decisions USING GIN (search);

ALTER TABLE events DROP COLUMN search;
ALTER TABLE events ADD COLUMN search TSVECTOR GENERATED ALWAYS AS (
    memory_search_vector(coalesce(payload ->> 'text', '') || ' ' || coalesce(payload ->> 'summary', ''))
) STORED;
CREATE INDEX events_search_idx ON events USING GIN (search);
