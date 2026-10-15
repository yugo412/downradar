-- 0005_vendors_from_the_table.sql
--
-- The services table becomes the source of truth for vendors. services.yaml
-- keeps only the adapter profiles, so the per vendor configuration that used to
-- live there moves into columns here.
--
-- profile is the link back to services.yaml: it names the adapter, the endpoint
-- and the default intervals. A row may override endpoint and intervals for
-- itself.

alter table services add column if not exists profile            text;
alter table services add column if not exists endpoint           text;
alter table services add column if not exists include_components text[] not null default '{}';
alter table services add column if not exists aliases            text[] not null default '{}';

create index if not exists services_profile_idx on services (profile);
