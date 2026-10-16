-- 0011_alert_subscriptions.sql
--
-- Email alerts. A visitor can ask to be told when a vendor changes status, and
-- the bell on the vendor page is where they ask. The address is all that is
-- kept: there is no account behind a subscription, and the token in the
-- unsubscribe link is the only handle on the row.
--
-- Sending is an outbox so that a mail server cannot hold up polling. When a
-- service changes status, a delivery row is written in the same transaction
-- that records the change, and a loop drains the pending rows over SMTP. The
-- change itself is copied into the row, so an alert describes the moment it
-- happened even if the status has moved on by the time it goes out.

create table alert_subscriptions (
  id              bigserial primary key,
  service_id      bigint not null references services(id) on delete cascade,
  -- The address is the subscription. user_id is only filled in when whoever
  -- subscribed happened to be signed in, and it stays null for the visitor who
  -- is not, which is the normal case. Deleting the account keeps the alert.
  user_id         bigint references users(id) on delete set null,
  email           text not null,
  token           text not null unique,
  unsubscribed_at timestamptz,
  created_at      timestamptz not null default now(),
  updated_at      timestamptz not null default now(),
  unique (service_id, email)
);

-- Unsubscribing keeps the row rather than deleting it, so the same address can
-- subscribe again to the same vendor without being refused by the unique
-- constraint above.

create index alert_subscriptions_pending_idx
  on alert_subscriptions (service_id) where unsubscribed_at is null;

create table alert_deliveries (
  id              bigserial primary key,
  subscription_id bigint not null references alert_subscriptions(id) on delete cascade,
  from_status     text not null,
  to_status       text not null,
  changed_at      timestamptz not null,
  attempts        integer not null default 0,
  error           text,
  sent_at         timestamptz,
  created_at      timestamptz not null default now()
);

-- The sender reads this index in order, so a delivery that is still waiting is
-- simply one with no sent_at yet.
create index alert_deliveries_pending_idx
  on alert_deliveries (id) where sent_at is null;
