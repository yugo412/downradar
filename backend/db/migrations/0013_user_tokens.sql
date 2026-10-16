-- 0013_user_tokens.sql
--
-- Accounts can now be made from the site rather than from the shell, so two
-- things are needed: whether an address has been proved to belong to the person
-- who typed it, and single use links for proving it and for resetting a
-- forgotten password.
--
-- Tokens are stored hashed, like session tokens, so a copy of the table is not
-- a set of working links. They are single use and they expire, and an expired
-- row is simply not found, so nothing depends on a cleanup job being correct.

alter table users add column verified_at timestamptz;

create table user_tokens (
  id         bigserial primary key,
  user_id    bigint not null references users(id) on delete cascade,
  kind       text not null check (kind in ('verify', 'reset')),
  token_hash text not null unique,
  expires_at timestamptz not null,
  used_at    timestamptz,
  created_at timestamptz not null default now()
);

-- The lookup is by token, which the unique index already covers. This one is for
-- the cleanup and for finding a person's outstanding links.
create index user_tokens_user_idx on user_tokens (user_id, kind) where used_at is null;
