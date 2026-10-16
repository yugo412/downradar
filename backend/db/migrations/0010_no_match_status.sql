-- 0010_no_match_status.sql
--
-- A vendor can now be saved before its platform is known. The row keeps the
-- name and the url, but no adapter profile reads the page yet, so it is not
-- polled. The status records that outcome, which is what tells the poller to
-- offer the page to the detection queue again later.
--
-- The constraint has to be replaced rather than added, so the old one is
-- dropped first. The column itself is unchanged.

alter table services drop constraint if exists services_status_check;

alter table services
  add constraint services_status_check check (status in ('proposed', 'published', 'rejected', 'no_match'));
