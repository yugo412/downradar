-- 0028_hidden_components.sql
--
-- Some status pages report a component that is not part of the service they
-- describe: an informational line, a link back to the page itself, or a group
-- that only exists so another platform can attach to it. Staff can take one of
-- those off the public page without turning the row off or waiting for the
-- source to stop reporting it. The component keeps being polled and keeps its
-- state, so showing it again brings its history back with it.

alter table components
  add column hidden boolean not null default false;
