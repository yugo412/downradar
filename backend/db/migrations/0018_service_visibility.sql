-- 0018_service_visibility.sql
--
-- Listing a service and owning it used to be the same question. owner_id was
-- set on a private row and empty on a catalogue one, so the public queries were
-- written as "owner_id is null". A subscriber can now add a service and choose
-- to publish it, which needs the two answers apart: visibility says who may see
-- the row, owner_id says who added it and looks after it.
--
-- Every row that exists keeps the answer it had: the owned ones are private and
-- the rest stay in the catalogue.

alter table services
  add column if not exists visibility text not null default 'public'
    check (visibility in ('public', 'private'));

update services set visibility = 'private' where owner_id is not null;

create index if not exists services_public_idx
  on services (visibility) where visibility = 'public';
