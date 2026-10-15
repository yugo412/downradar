-- 0006_proposed_services.sql
--
-- A vendor row can now be a proposal rather than a live entry. The suggest form
-- writes a row with status 'proposed' instead of opening a GitHub issue, and a
-- moderator publishes it later.
--
-- url becomes nullable because a proposal may not carry one: the suggest form
-- asks for a name and treats the url as optional.

alter table services add column if not exists status text not null default 'published';

do $$
begin
  alter table services
    add constraint services_status_check check (status in ('proposed', 'published', 'rejected'));
exception
  when duplicate_object then null;
end $$;

-- Everything that existed before this migration is live.
update services set status = 'published' where status is null;

alter table services alter column url drop not null;

create index if not exists services_status_idx on services (status, active);
