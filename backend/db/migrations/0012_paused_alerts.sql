-- 0012_paused_alerts.sql
--
-- A subscription can now be paused as well as ended. Pausing is for somebody who
-- wants a quiet week rather than a goodbye: the row keeps its place, the sender
-- skips it, and the account screen offers to start it again. Unsubscribing stays
-- what the link in an email does, and what the vendor page's button does.
--
-- Both are timestamps rather than a state column, because "when did this stop"
-- is the useful thing to know and the state is what the two of them say.

alter table alert_subscriptions add column paused_at timestamptz;

-- The index the sender and the status change read is now "rows that will
-- receive", so it has to leave paused ones out as well.
drop index if exists alert_subscriptions_pending_idx;

create index alert_subscriptions_pending_idx
  on alert_subscriptions (service_id)
  where unsubscribed_at is null and paused_at is null;
