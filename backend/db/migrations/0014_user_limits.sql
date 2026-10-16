-- Per-user limits.
--
-- A limit used to be one number for every subscriber, read straight from the
-- environment. It now lives in a row, so one account can differ from another.
-- The environment is still where a new account's starting value comes from, and
-- the row is what is read afterwards, so a value can be changed without a
-- redeploy.

create table user_limits (
    user_id    bigint      not null references users (id) on delete cascade,
    kind       text        not null,
    max_value  int         not null check (max_value >= 0),
    updated_at timestamptz not null default now(),
    primary key (user_id, kind)
);

-- Accounts made before the table existed get the value that used to apply to
-- everybody. It is a starting point rather than a value anything depends on: the
-- code default is what a new account is given, and this backfill is a snapshot.
insert into user_limits (user_id, kind, max_value)
select u.id, d.kind, d.max_value
from users u
cross join (values ('alert', 10)) as d (kind, max_value)
on conflict (user_id, kind) do nothing;
