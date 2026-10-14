DROP TRIGGER job_metrics ON jobs;
DROP FUNCTION observe_job_metrics();
DROP TABLE job_state_metric_totals;
DROP TABLE job_metric_totals;
DROP INDEX jobs_metrics_active_idx;
ALTER TABLE jobs DROP COLUMN state_entered_at;
ALTER TABLE jobs DROP COLUMN metrics_directory_id;
ALTER TABLE jobs DROP COLUMN metrics_directory_frozen;
