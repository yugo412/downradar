-- 0026_detection_attempts.sql
--
-- A page no profile could read is offered to the detection queue again on the
-- next hourly pass. The count is how many attempts in a row have failed since
-- the last manual start; once it reaches the limit the row is paused, so a wrong
-- address or a platform without an adapter is not asked forever.

alter table services add column if not exists detection_attempts integer not null default 0;
