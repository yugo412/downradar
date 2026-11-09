-- 0030_team_webhook_calls.sql
--
-- A webhook call belongs to a person or a team, never both, the same way an
-- alert subscription does. A team alert's calls are shown to every member,
-- because the team is who the alert belongs to; before this column they had no
-- owner to attach to and were not written down at all.

alter table alert_webhook_calls alter column user_id drop not null;
alter table alert_webhook_calls add column team_id bigint references teams (id) on delete cascade;
alter table alert_webhook_calls add constraint alert_webhook_calls_one_owner
    check (num_nonnulls(user_id, team_id) = 1);

create index alert_webhook_calls_team_idx
    on alert_webhook_calls (team_id, service_id, id desc) where team_id is not null;
