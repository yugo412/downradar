-- 0016_alert_targets_subscribers_idx.sql
--
-- A vendor page says how many people are watching it, which is every target on
-- that service, a component of it included. The index the sender reads is
-- partial, on the whole-vendor targets only, so the count gets one of its own.

create index alert_targets_subscribers_idx on alert_targets (service_id);
