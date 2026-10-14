-- Counters are updated by the same transaction as the job, including backend
-- reconciliation. No event replay, process restart or rollback can count twice.
ALTER TABLE jobs ADD COLUMN metrics_directory_id UUID;
ALTER TABLE jobs ADD COLUMN metrics_directory_frozen BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE jobs ADD COLUMN state_entered_at TIMESTAMPTZ NOT NULL DEFAULT now();

-- Historical directories and state durations cannot be reconstructed. Existing
-- jobs start observation at migration time, with their currently configured cwd.
UPDATE jobs j SET metrics_directory_id = s.working_directory_id,
    metrics_directory_frozen = (j.status <> 'QUEUED')
FROM runs r JOIN sessions s ON s.id = r.session_id WHERE r.id = j.run_id;

CREATE TABLE job_metric_totals (
    project_id TEXT NOT NULL,
    backend_instance_id TEXT NOT NULL,
    known_directory_id TEXT NOT NULL,
    outcome TEXT NOT NULL,
    total BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (project_id, backend_instance_id, known_directory_id, outcome)
);

CREATE TABLE job_state_metric_totals (
    project_id TEXT NOT NULL,
    backend_instance_id TEXT NOT NULL,
    known_directory_id TEXT NOT NULL,
    state TEXT NOT NULL,
    exits BIGINT NOT NULL DEFAULT 0,
    seconds DOUBLE PRECISION NOT NULL DEFAULT 0,
    PRIMARY KEY (project_id, backend_instance_id, known_directory_id, state)
);

-- Deliberately no foreign keys on aggregate identities: deleting a project,
-- directory or completed job must not decrement a Prometheus counter.
CREATE FUNCTION observe_job_metrics() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    pid TEXT;
    bid TEXT;
    did UUID;
    observed_at TIMESTAMPTZ := clock_timestamp();
BEGIN
    IF TG_OP = 'UPDATE' AND NEW.status = OLD.status THEN
        RETURN NEW;
    END IF;
    SELECT s.project_id::text, r.backend_instance_id::text, s.working_directory_id
    INTO pid, bid, did FROM runs r JOIN sessions s ON s.id = r.session_id
    WHERE r.id = NEW.run_id;

    IF TG_OP = 'INSERT' THEN
        NEW.metrics_directory_id := did;
        NEW.metrics_directory_frozen := NEW.status <> 'QUEUED' AND NEW.status <> 'WAITING_BACKEND';
        NEW.state_entered_at := observed_at;
    ELSE
        -- Resolve the cwd again at first dispatch; queued jobs follow explicit
        -- session changes. Once execution starts the attribution is immutable.
        IF NOT OLD.metrics_directory_frozen THEN
            NEW.metrics_directory_id := did;
        END IF;
        IF NEW.status = 'RUNNING' OR NEW.status IN ('COMPLETED', 'FAILED', 'CANCELLED') THEN
            NEW.metrics_directory_frozen := true;
        END IF;
        INSERT INTO job_state_metric_totals AS totals
            (project_id, backend_instance_id, known_directory_id, state, exits, seconds)
        VALUES (pid, bid, COALESCE(NEW.metrics_directory_id::text, ''), OLD.status,
            1, GREATEST(0, EXTRACT(EPOCH FROM observed_at - OLD.state_entered_at)))
        ON CONFLICT (project_id, backend_instance_id, known_directory_id, state)
        DO UPDATE SET exits = totals.exits + 1, seconds = totals.seconds + EXCLUDED.seconds;
        NEW.state_entered_at := observed_at;
    END IF;

    IF NEW.status IN ('COMPLETED', 'FAILED', 'CANCELLED') THEN
        INSERT INTO job_metric_totals AS totals
            (project_id, backend_instance_id, known_directory_id, outcome, total)
        VALUES (pid, bid, COALESCE(NEW.metrics_directory_id::text, ''), NEW.status, 1)
        ON CONFLICT (project_id, backend_instance_id, known_directory_id, outcome)
        DO UPDATE SET total = totals.total + 1;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER job_metrics BEFORE INSERT OR UPDATE OF status ON jobs
FOR EACH ROW EXECUTE FUNCTION observe_job_metrics();

CREATE INDEX jobs_metrics_active_idx ON jobs (run_id, status)
WHERE status NOT IN ('COMPLETED', 'FAILED', 'CANCELLED');
