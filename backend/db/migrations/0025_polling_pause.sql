-- 0025_polling_pause.sql
--
-- Staff can stop asking a vendor's page without taking the vendor off the site.
-- The row keeps its last known state and its history; only the poller leaves it
-- alone. Resuming lets the normal interval pick it up again.

alter table services add column if not exists paused boolean not null default false;
