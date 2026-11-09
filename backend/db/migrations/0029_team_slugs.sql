-- 0029_team_slugs.sql
--
-- A team's address is its name, not its id, so a link reads as /teams/acme.
-- Existing rows get a stable placeholder made from their id; teams created from
-- now on get a slug from their name.

alter table teams add column slug text;

update teams set slug = 'team-' || id where slug is null;

alter table teams alter column slug set not null;
create unique index teams_slug_key on teams (slug);
