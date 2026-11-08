-- What Jobs consumed, by Session, backend, model and effort, and why Jobs
-- waited for their backend. Both are read from the events that end a Job or
-- stop an attempt, in the transaction that appends them: an event replayed by a
-- backend is not appended twice (ON CONFLICT DO NOTHING fires no AFTER trigger)
-- and so is not counted twice either.
--
-- Unlike state durations, this history survives in the events, so it is
-- rebuilt below rather than observed from the migration on.

-- Prometheus counters. Deliberately no foreign keys, as in job_metric_totals:
-- deleting a Session must not decrement a counter.
CREATE TABLE job_usage_metric_totals (
    project_id          TEXT NOT NULL,
    session_id          TEXT NOT NULL,
    backend_instance_id TEXT NOT NULL,
    model               TEXT NOT NULL,
    effort              TEXT NOT NULL,
    outcome             TEXT NOT NULL,
    jobs                BIGINT NOT NULL DEFAULT 0,
    -- Jobs whose backend reported usage. The others consumed something too,
    -- but nobody knows what: the gap is worth seeing.
    reported_jobs       BIGINT NOT NULL DEFAULT 0,
    input_tokens        BIGINT NOT NULL DEFAULT 0,
    output_tokens       BIGINT NOT NULL DEFAULT 0,
    cache_read_tokens   BIGINT NOT NULL DEFAULT 0,
    cache_write_tokens  BIGINT NOT NULL DEFAULT 0,
    cost_usd            DOUBLE PRECISION NOT NULL DEFAULT 0,
    PRIMARY KEY (project_id, session_id, backend_instance_id, model, effort, outcome)
);

-- The same consumption by UTC day, for the usage API. It follows its Session:
-- a deleted Session no longer shows in a ranking.
CREATE TABLE session_usage_daily (
    day                 DATE NOT NULL,
    session_id          UUID NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    backend_instance_id TEXT NOT NULL,
    model               TEXT NOT NULL,
    effort              TEXT NOT NULL,
    jobs                BIGINT NOT NULL DEFAULT 0,
    reported_jobs       BIGINT NOT NULL DEFAULT 0,
    input_tokens        BIGINT NOT NULL DEFAULT 0,
    output_tokens       BIGINT NOT NULL DEFAULT 0,
    cache_read_tokens   BIGINT NOT NULL DEFAULT 0,
    cache_write_tokens  BIGINT NOT NULL DEFAULT 0,
    cost_usd            DOUBLE PRECISION NOT NULL DEFAULT 0,
    PRIMARY KEY (day, session_id, backend_instance_id, model, effort)
);
CREATE INDEX session_usage_daily_session_idx ON session_usage_daily (session_id, day);

CREATE TABLE job_wait_metric_totals (
    project_id          TEXT NOT NULL,
    backend_instance_id TEXT NOT NULL,
    reason              TEXT NOT NULL,
    total               BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (project_id, backend_instance_id, reason)
);

-- One row per event, already resolved to its labels and figures. The
-- trigger and the rebuild below share it, so they cannot disagree.
CREATE FUNCTION job_usage_of(e events) RETURNS TABLE (
    project_id TEXT, session_id UUID, backend_instance_id TEXT, model TEXT, effort TEXT,
    outcome TEXT, reported BIGINT, input_tokens BIGINT, output_tokens BIGINT,
    cache_read_tokens BIGINT, cache_write_tokens BIGINT, cost_usd DOUBLE PRECISION, day DATE
) LANGUAGE sql STABLE AS $$
    SELECT e.project_id::text, e.session_id,
        COALESCE(r.backend_instance_id::text, e.backend_instance_id::text, ''),
        COALESCE(NULLIF(e.payload->>'model', ''), j.model, ''),
        COALESCE(NULLIF(e.payload->>'effort', ''), j.effort, ''),
        CASE e.type WHEN 'job.completed' THEN 'COMPLETED' WHEN 'job.failed' THEN 'FAILED' ELSE 'CANCELLED' END,
        (e.payload ? 'usage')::int::bigint,
        COALESCE((e.payload->'usage'->>'inputTokens')::bigint, 0),
        COALESCE((e.payload->'usage'->>'outputTokens')::bigint, 0),
        COALESCE((e.payload->'usage'->>'cacheReadTokens')::bigint, 0),
        COALESCE((e.payload->'usage'->>'cacheWriteTokens')::bigint, 0),
        COALESCE((e.payload->'usage'->>'costUsd')::float8, 0),
        (e.occurred_at AT TIME ZONE 'UTC')::date
    FROM (SELECT 1) one
    LEFT JOIN runs r ON r.id = e.run_id
    LEFT JOIN jobs j ON j.id = e.job_id
$$;

CREATE FUNCTION observe_job_usage() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    u RECORD;
    bid TEXT;
BEGIN
    IF NEW.project_id IS NULL OR NEW.session_id IS NULL THEN
        RETURN NULL;
    END IF;
    IF NEW.type = 'job.waiting_backend' THEN
        SELECT COALESCE(r.backend_instance_id::text, NEW.backend_instance_id::text, '') INTO bid
        FROM (SELECT 1) one LEFT JOIN runs r ON r.id = NEW.run_id;
        INSERT INTO job_wait_metric_totals AS t (project_id, backend_instance_id, reason, total)
        VALUES (NEW.project_id::text, bid, COALESCE(NULLIF(NEW.payload->>'reason', ''), 'UNKNOWN'), 1)
        ON CONFLICT (project_id, backend_instance_id, reason) DO UPDATE SET total = t.total + 1;
        RETURN NULL;
    END IF;

    SELECT * INTO u FROM job_usage_of(NEW);
    INSERT INTO job_usage_metric_totals AS t
        (project_id, session_id, backend_instance_id, model, effort, outcome, jobs, reported_jobs,
         input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, cost_usd)
    VALUES (u.project_id, u.session_id::text, u.backend_instance_id, u.model, u.effort, u.outcome, 1, u.reported,
        u.input_tokens, u.output_tokens, u.cache_read_tokens, u.cache_write_tokens, u.cost_usd)
    ON CONFLICT (project_id, session_id, backend_instance_id, model, effort, outcome) DO UPDATE SET
        jobs = t.jobs + 1, reported_jobs = t.reported_jobs + EXCLUDED.reported_jobs,
        input_tokens = t.input_tokens + EXCLUDED.input_tokens,
        output_tokens = t.output_tokens + EXCLUDED.output_tokens,
        cache_read_tokens = t.cache_read_tokens + EXCLUDED.cache_read_tokens,
        cache_write_tokens = t.cache_write_tokens + EXCLUDED.cache_write_tokens,
        cost_usd = t.cost_usd + EXCLUDED.cost_usd;
    INSERT INTO session_usage_daily AS t
        (day, session_id, backend_instance_id, model, effort, jobs, reported_jobs,
         input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, cost_usd)
    VALUES (u.day, u.session_id, u.backend_instance_id, u.model, u.effort, 1, u.reported,
        u.input_tokens, u.output_tokens, u.cache_read_tokens, u.cache_write_tokens, u.cost_usd)
    ON CONFLICT (day, session_id, backend_instance_id, model, effort) DO UPDATE SET
        jobs = t.jobs + 1, reported_jobs = t.reported_jobs + EXCLUDED.reported_jobs,
        input_tokens = t.input_tokens + EXCLUDED.input_tokens,
        output_tokens = t.output_tokens + EXCLUDED.output_tokens,
        cache_read_tokens = t.cache_read_tokens + EXCLUDED.cache_read_tokens,
        cache_write_tokens = t.cache_write_tokens + EXCLUDED.cache_write_tokens,
        cost_usd = t.cost_usd + EXCLUDED.cost_usd;
    RETURN NULL;
END;
$$;

CREATE TRIGGER job_usage_metrics AFTER INSERT ON events
FOR EACH ROW WHEN (NEW.type IN ('job.completed', 'job.failed', 'job.cancelled', 'job.waiting_backend'))
EXECUTE FUNCTION observe_job_usage();

-- Rebuild from the history. Events of deleted Sessions went with them.
INSERT INTO job_usage_metric_totals
    (project_id, session_id, backend_instance_id, model, effort, outcome, jobs, reported_jobs,
     input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, cost_usd)
SELECT u.project_id, u.session_id::text, u.backend_instance_id, u.model, u.effort, u.outcome,
    count(*), sum(u.reported), sum(u.input_tokens), sum(u.output_tokens),
    sum(u.cache_read_tokens), sum(u.cache_write_tokens), sum(u.cost_usd)
FROM events e CROSS JOIN LATERAL job_usage_of(e) u
WHERE e.type IN ('job.completed', 'job.failed', 'job.cancelled')
  AND e.project_id IS NOT NULL AND e.session_id IS NOT NULL
GROUP BY 1, 2, 3, 4, 5, 6;

INSERT INTO session_usage_daily
    (day, session_id, backend_instance_id, model, effort, jobs, reported_jobs,
     input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, cost_usd)
SELECT u.day, u.session_id, u.backend_instance_id, u.model, u.effort,
    count(*), sum(u.reported), sum(u.input_tokens), sum(u.output_tokens),
    sum(u.cache_read_tokens), sum(u.cache_write_tokens), sum(u.cost_usd)
FROM events e CROSS JOIN LATERAL job_usage_of(e) u
WHERE e.type IN ('job.completed', 'job.failed', 'job.cancelled')
  AND e.project_id IS NOT NULL AND e.session_id IS NOT NULL
GROUP BY 1, 2, 3, 4, 5;

INSERT INTO job_wait_metric_totals (project_id, backend_instance_id, reason, total)
SELECT e.project_id::text, COALESCE(r.backend_instance_id::text, e.backend_instance_id::text, ''),
    COALESCE(NULLIF(e.payload->>'reason', ''), 'UNKNOWN'), count(*)
FROM events e LEFT JOIN runs r ON r.id = e.run_id
WHERE e.type = 'job.waiting_backend' AND e.project_id IS NOT NULL AND e.session_id IS NOT NULL
GROUP BY 1, 2, 3;
