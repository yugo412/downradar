-- 0026_notifications.sql
--
-- One feed for everything that is not tied to a single vendor page. A notice is
-- written once, and the accounts that should see it get a recipient row of their
-- own. That row is also where read state lives, so opening the feed does not
-- change what anybody else sees.
--
-- A notice can point at a service or an alert. The links are nullable and clear
-- when the thing they point at goes away, because the notice is still part of
-- the history even after its subject is gone.
--
-- dedupe_key is set when one event can arrive through more than one alert
-- channel, so the feed still gets one notice. Staff notices leave it null.

create table if not exists notifications (
  id         bigserial   primary key,
  audience   text        not null check (audience in ('staff', 'subscribers')),
  title      text        not null check (title <> ''),
  body       text        not null check (body <> ''),
  service_id bigint      references services (id) on delete set null,
  alert_id   bigint      references alert_subscriptions (id) on delete set null,
  created_by bigint      references users (id) on delete set null,
  dedupe_key text        unique,
  created_at timestamptz not null default now()
);

create index if not exists notifications_created_idx
  on notifications (created_at desc, id desc);

create table if not exists notification_recipients (
  notification_id bigint      not null references notifications (id) on delete cascade,
  user_id         bigint      not null references users (id) on delete cascade,
  read_at         timestamptz,
  created_at      timestamptz not null default now(),
  primary key (notification_id, user_id)
);

-- The feed reads one account's rows newest first, and the bell only needs the
-- unread ones.
create index if not exists notification_recipients_feed_idx
  on notification_recipients (user_id, notification_id desc);
create index if not exists notification_recipients_unread_idx
  on notification_recipients (user_id) where read_at is null;
