-- A Core Tool that requires validation consumes the person's approval when
-- Core accepts the call, so one approval covers exactly one call.
ALTER TABLE validation_requests ADD COLUMN receipt_used_at TIMESTAMPTZ;
