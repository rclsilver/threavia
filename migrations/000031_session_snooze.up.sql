-- Snooze: a person puts a Session out of the way until a time, or until the
-- agent says something new. snoozed_sequence is how far the Session had gone
-- when it was snoozed, which is what "something new" is measured from. A
-- Session whose snooze ended stays marked until it is opened, so it comes back
-- as unread instead of quietly reappearing in the list.
ALTER TABLE sessions
  ADD COLUMN snoozed_until TIMESTAMPTZ,
  ADD COLUMN snooze_until_change BOOLEAN NOT NULL DEFAULT false,
  ADD COLUMN snoozed_sequence BIGINT;
