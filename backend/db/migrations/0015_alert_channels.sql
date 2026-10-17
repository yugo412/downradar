-- 0015_alert_channels.sql
--
-- An alert used to be one row that was three things at once: somebody who wants
-- to hear about a vendor, the address the message goes to, and the token that
-- stops it. It is three rows now.
--
-- A subscription is the first. What it watches is an alert_target, which is a
-- vendor's overall status today and can be one of its components later. How it
-- reaches its owner is an alert_channel: an address today, a webhook or a chat
-- service later, each with a token of its own so a link stops the channel it
-- came from rather than everything the person has.
--
-- Nothing is lost in the move. Every subscription that exists keeps its address,
-- its token and its state as a channel, and a delivery that was still waiting
-- points at the channel it now belongs to.

create table alert_targets (
    id              bigserial   primary key,
    subscription_id bigint      not null references alert_subscriptions (id) on delete cascade,
    service_id      bigint      not null references services (id) on delete cascade,
    -- Null is the vendor's overall status; a component follows one server
    -- rather than the platform it sits on.
    component_id    bigint      references components (id) on delete cascade,
    created_at      timestamptz not null default now()
);

-- A null is not equal to another null, so "the vendor's overall status" needs an
-- index of its own to stay unique within one subscription.
create unique index alert_targets_service_uniq
    on alert_targets (subscription_id, service_id) where component_id is null;
create unique index alert_targets_component_uniq
    on alert_targets (subscription_id, component_id) where component_id is not null;

-- What a status change reads to find out who has to be told.
create index alert_targets_service_idx
    on alert_targets (service_id) where component_id is null;
create index alert_targets_component_idx
    on alert_targets (component_id) where component_id is not null;

create table alert_channels (
    id              bigserial   primary key,
    subscription_id bigint      not null references alert_subscriptions (id) on delete cascade,
    -- Every channel is the same three things: where it goes, what it needs, and
    -- a token that stops it. Email is the only kind so far; the settings are
    -- where a webhook's headers or a bot's secret will live.
    kind            text        not null,
    address         text        not null,
    settings        jsonb       not null default '{}',
    token           text        not null unique,
    paused_at       timestamptz,
    unsubscribed_at timestamptz,
    created_at      timestamptz not null default now(),
    updated_at      timestamptz not null default now(),
    unique (subscription_id, kind, address),
    check (address <> '')
);

create index alert_channels_subscription_idx on alert_channels (subscription_id);

-- The address is the whole identity of a visitor with no account, so it has to
-- be findable without walking every channel there is.
create index alert_channels_email_idx
    on alert_channels (address) where kind = 'email' and unsubscribed_at is null;

-- The account screen reads a person's subscriptions, and the limit counts them.
create index alert_subscriptions_user_idx
    on alert_subscriptions (user_id) where user_id is not null;

-- Each row that exists was one address on one vendor, which is exactly one
-- target and one email channel.
insert into alert_targets (subscription_id, service_id)
select id, service_id from alert_subscriptions;

insert into alert_channels (subscription_id, kind, address, token,
                            paused_at, unsubscribed_at, created_at, updated_at)
select id, 'email', email, token, paused_at, unsubscribed_at, created_at, updated_at
from alert_subscriptions;

alter table alert_deliveries
    add column channel_id bigint references alert_channels (id) on delete cascade,
    add column target_id  bigint references alert_targets (id) on delete cascade;

-- A delivery that was waiting for an address is waiting for that channel, and it
-- says which thing changed rather than which subscription asked.
update alert_deliveries d
set channel_id = c.id,
    target_id  = t.id
from alert_channels c
join alert_targets t on t.subscription_id = c.subscription_id
where c.subscription_id = d.subscription_id;

alter table alert_deliveries
    alter column channel_id set not null,
    alter column target_id  set not null,
    drop column subscription_id;

drop index if exists alert_subscriptions_pending_idx;

-- What is left of a subscription is who wants to hear, and nothing else: the
-- state is what its channels say, and the address and the token belong to them.
alter table alert_subscriptions
    drop column service_id,
    drop column email,
    drop column token,
    drop column paused_at,
    drop column unsubscribed_at;
