-- 0021_user_providers.sql
--
-- One row per account a provider vouches for.
--
-- A person may sign in with more than one provider, so the link is a row of its
-- own rather than a column on users: GitHub today, another one later, and the
-- account stays the same. The provider's own id (subject) is what the row is
-- found by, because an address or a username can change at either end and the
-- id cannot. The address and the username are stored beside it as what the
-- provider said at the time, not as the account's own values.

create table if not exists user_providers (
    id            bigserial   primary key,
    user_id       bigint      not null references users (id) on delete cascade,
    provider      text        not null,
    subject       text        not null,
    email         text        not null default '',
    username      text        not null default '',
    created_at    timestamptz not null default now(),
    last_login_at timestamptz,
    -- One identity at a provider is one account, and one account has at most one
    -- identity at each provider. A second GitHub account on the same address is
    -- refused rather than silently becoming a second link.
    constraint user_providers_provider_key unique (provider, subject),
    constraint user_providers_user_key unique (user_id, provider)
);
