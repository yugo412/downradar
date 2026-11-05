-- 0028_teams.sql
--
-- A team is a workspace beside a person's own rows. It has members with one of
-- three roles and a single owner, who is the only one who can hand it on. Rows
-- that belong to a team carry its id instead of a person's, never both, so an
-- owner can always be resolved without guessing.

create table teams (
    id         bigserial   primary key,
    name       text        not null check (btrim(name) <> ''),
    -- The creator is kept for audit. What authorization reads is the role in
    -- team_members, because ownership can be transferred.
    created_by bigint      references users (id) on delete set null,
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now()
);

create table team_members (
    team_id    bigint      not null references teams (id) on delete cascade,
    user_id    bigint      not null references users (id) on delete cascade,
    role       text        not null check (role in ('owner', 'manager', 'member')),
    created_at timestamptz not null default now(),
    primary key (team_id, user_id)
);

-- At most one owner per team. Creating a team writes that row in the same
-- transaction, and ownership is transferred rather than shared.
create unique index team_members_one_owner on team_members (team_id) where role = 'owner';
create index team_members_user_idx on team_members (user_id);

create table team_invitations (
    id          bigserial   primary key,
    team_id     bigint      not null references teams (id) on delete cascade,
    email       text        not null check (btrim(email) <> ''),
    role        text        not null check (role in ('owner', 'manager', 'member')),
    -- The link's own token is kept hashed, like a session or a sign in link.
    token_hash  text        not null unique,
    invited_by  bigint      references users (id) on delete set null,
    -- An owner invitation is the transfer path: the old owner steps down when
    -- the new one accepts.
    transfer    boolean     not null default false,
    expires_at  timestamptz not null,
    accepted_at timestamptz,
    created_at  timestamptz not null default now(),
    check ((role = 'owner') = transfer)
);

-- One pending invitation per address on a team. Inviting again replaces the
-- old link instead of leaving two live ones.
create unique index team_invitations_pending_email
    on team_invitations (team_id, lower(email)) where accepted_at is null;
create index team_invitations_team_idx on team_invitations (team_id) where accepted_at is null;

-- A service or an alert belongs to one person or one team, never both. Existing
-- rows keep their person and stay exactly as they were.
alter table services add column team_id bigint references teams (id) on delete cascade;
create index services_team_idx on services (team_id) where team_id is not null;
alter table services add constraint services_one_owner
    check (num_nonnulls(owner_id, team_id) <= 1);

alter table alert_subscriptions add column team_id bigint references teams (id) on delete cascade;
create unique index alert_subscriptions_team_service_uniq
    on alert_subscriptions (service_id, team_id) where team_id is not null;
create index alert_subscriptions_team_idx on alert_subscriptions (team_id) where team_id is not null;
alter table alert_subscriptions add constraint alert_subscriptions_one_owner
    check (num_nonnulls(user_id, team_id) <= 1);
