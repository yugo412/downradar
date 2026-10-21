-- 0020_poll_backoff.sql
--
-- A page that answers 429 or 503 is asking not to be asked again for a while.
-- Answering on the next tick is the quickest way to turn a throttle into a
-- blocked address, so the wait is remembered on the vendor's row: next_poll_at
-- is the earliest the poller may call again, and poll_refusals counts the
-- refusals in a row, which is what makes the wait grow. A page that answers 403
-- because the caller looks like a datacenter is put on a slow schedule the same
-- way. Both columns are cleared by the first poll that works.

alter table services
  add column if not exists next_poll_at timestamptz,
  add column if not exists poll_refusals integer not null default 0;
