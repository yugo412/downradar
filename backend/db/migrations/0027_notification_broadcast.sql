-- 0027_notification_broadcast.sql
--
-- A site-wide announcement goes to every registered account, staff included.
-- The audience row makes that a property of the notice rather than a list of
-- recipient ids copied at the time it was written.

alter table notifications
  drop constraint if exists notifications_audience_check;

alter table notifications
  add constraint notifications_audience_check
  check (audience in ('staff', 'subscribers', 'everyone'));
