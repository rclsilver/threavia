ALTER TABLE sessions
  DROP COLUMN IF EXISTS snoozed_sequence,
  DROP COLUMN IF EXISTS snooze_until_change,
  DROP COLUMN IF EXISTS snoozed_until;
