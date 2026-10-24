-- 0023_alert_channel_status.sql
--
-- The page that sets a channel up should be able to say how its last real
-- delivery went. A webhook has its own call log; the other ways only need the
-- last attempt, so this stays three columns rather than another history table.
--
-- The columns describe deliveries, not tests: trying a channel from the page
-- does not touch them.

alter table alert_channels
    add column last_attempt_at timestamptz,
    add column last_success_at timestamptz,
    add column last_error      text;
