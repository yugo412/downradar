-- 0007_users_and_sessions.sql
--
-- People who may sign in, and the sessions their browsers carry.
--
-- There is no registration form: the first admin is created with the
-- downradar-user command. The subscriber role exists so the vocabulary does not
-- have to change later; nothing uses it yet.
--
-- Only the hash of a session token is stored. A leaked database dump therefore
-- does not hand out working sessions.

create table if not exists users (
    id            bigserial   primary key,
    email         text        not null,
    name          text        not null,
    password_hash text        not null,
    role          text        not null,
    created_at    timestamptz not null default now(),
    updated_at    timestamptz not null default now()
);

-- Addresses are compared case insensitively everywhere, so uniqueness has to
-- be case insensitive too, otherwise Foo@x and foo@x can both exist.
create unique index if not exists users_email_key on users (lower(email));

do $$
begin
  alter table users
    add constraint users_role_check check (role in ('admin', 'moderator', 'subscriber'));
exception
  when duplicate_object then null;
end $$;

create table if not exists sessions (
    token_hash text        primary key,
    user_id    bigint      not null references users (id) on delete cascade,
    expires_at timestamptz not null,
    created_at timestamptz not null default now()
);

create index if not exists sessions_user_idx on sessions (user_id);
create index if not exists sessions_expires_idx on sessions (expires_at);
