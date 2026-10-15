-- 0002_logo_url.sql
-- Optional per-vendor logo, curated in services.yaml.
--
-- Like every other column on services, this is written by SyncServices from
-- the registry, so changing it in the YAML and reloading is enough. The column
-- is nullable: a vendor without a logo falls back to a generated placeholder
-- on the site.

alter table services add column if not exists logo_url text;
