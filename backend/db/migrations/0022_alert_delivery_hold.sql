-- 0022_alert_delivery_hold.sql
--
-- A change is written as an observation immediately, but its delivery waits
-- until the new state has held for a short window. A second change before then
-- updates the same pending row, so a burst becomes one message while the event
-- history keeps every transition.

alter table alert_deliveries
  add column if not exists deliver_after timestamptz not null default now();

-- One pending delivery per channel and target is what lets the writer update it
-- in place. Older duplicates, if any, are dropped in favour of the newest.
delete from alert_deliveries d
using alert_deliveries newer
where d.sent_at is null
  and newer.sent_at is null
  and d.channel_id = newer.channel_id
  and d.service_id = newer.service_id
  and d.component_id is not distinct from newer.component_id
  and d.id < newer.id;

create unique index if not exists alert_deliveries_pending_target_idx
  on alert_deliveries (channel_id, service_id, coalesce(component_id, 0))
  where sent_at is null;

create index if not exists alert_deliveries_due_idx
  on alert_deliveries (deliver_after)
  where sent_at is null;
