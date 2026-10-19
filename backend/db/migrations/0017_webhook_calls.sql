-- 0017_webhook_calls.sql
--
-- A webhook is a call out to somebody else's end. When it fails the subscriber
-- has nothing to look at: the failure is a log line on this side and silence on
-- theirs, and the only way to find out was to go and read our logs, which are
-- not theirs to read.
--
-- This is the table that answers "did it call, and what did it say". It holds
-- what the sender tried, what the far end answered, and the error when the call
-- never got there, for the last few calls of one alert. A call the page tries
-- before anything is saved lands here too, which is why the channel is optional:
-- a try has no channel yet.
--
-- The body that was sent and the headers are not kept. A header list carries the
-- channel's own bearer token, and a table that holds tokens is a table worth
-- stealing; what a call carried is the same for every call anyway.

create table alert_webhook_calls (
    id         bigserial   primary key,
    -- Who was shown this and which service it was about, which is how the alert
    -- page finds its own rows. A subscription whose owner is gone has nobody to
    -- show a log to, so nothing is written for it.
    user_id    bigint      not null references users (id) on delete cascade,
    service_id bigint      not null references services (id) on delete cascade,
    -- The way the call went out by, when it went out for one.
    channel_id bigint      references alert_channels (id) on delete cascade,
    -- A call the sender made for a change, or one somebody tried from the page.
    kind       text        not null,
    url        text        not null,
    method     text        not null,
    -- What the far end answered. Null is a call that was never answered, which
    -- is what the error beside it says.
    status     integer,
    response   text        not null default '',
    error      text        not null default '',
    created_at timestamptz not null default now(),
    check (kind in ('delivery', 'test'))
);

-- The page reads the last few calls of one alert on one service, newest first,
-- which is this index read backwards.
create index alert_webhook_calls_alert_idx
    on alert_webhook_calls (user_id, service_id, id desc);
