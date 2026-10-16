-- 0008_service_owner.sql
--
-- A service can belong to one person instead of to the public catalogue. A row
-- with owner_id set is private: it is still polled, but it is left out of the
-- public list, the vendor pages, the stats and the sitemap, and only its owner
-- and the staff can see it.
--
-- Visibility is a separate question from status. status says whether a row is
-- polled (proposed / published / rejected); owner_id says who may see it. A
-- private service is published, because there is nothing to moderate.
--
-- Deleting an account takes its private services with it. That is deliberate:
-- the alternative is owner_id becoming null, which would quietly move somebody's
-- private service into the public catalogue.

alter table services
  add column if not exists owner_id bigint references users (id) on delete cascade;

create index if not exists services_owner_idx on services (owner_id) where owner_id is not null;
