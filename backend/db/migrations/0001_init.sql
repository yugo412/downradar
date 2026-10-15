-- 0001_init.sql
-- Initial schema for downradar.info.
--
-- Design principle: vendor definitions live in services.yaml (written by
-- humans), while the database only stores what the machine DISCOVERS (state,
-- history, parser health). That is why there are no selector or endpoint
-- columns here.
--
-- Nothing in this file is dropped or rewritten by the application: the
-- services table is only ever upserted, and vendors that disappear from
-- services.yaml are soft deleted so their history survives.

create table if not exists services (
    id                        bigserial primary key,
    slug                      text        not null unique,
    name                      text        not null,
    url                       text        not null,
    category                  text,
    active                    boolean     not null default true,
    poll_interval_seconds     integer     not null default 300,
    active_interval_seconds   integer     not null default 60,
    last_polled_at            timestamptz,
    created_at                timestamptz not null default now(),
    updated_at                timestamptz not null default now()
);

create index if not exists services_active_idx on services (active) where active;

create table if not exists components (
    id          bigserial primary key,
    service_id  bigint      not null references services (id) on delete cascade,
    source_id   text        not null,
    name        text        not null,
    group_name  text,
    position    integer     not null default 0,
    active      boolean     not null default true,
    created_at  timestamptz not null default now(),
    updated_at  timestamptz not null default now(),
    unique (service_id, source_id)
);

create index if not exists components_service_idx on components (service_id) where active;

-- Current state. Read on every page view, so it must stay cheap.
create table if not exists service_state (
    service_id  bigint      primary key references services (id) on delete cascade,
    status      text        not null,
    changed_at  timestamptz not null,
    updated_at  timestamptz not null default now()
);

create table if not exists component_state (
    component_id bigint      primary key references components (id) on delete cascade,
    status       text        not null,
    changed_at   timestamptz not null,
    incident_id  text,
    updated_at   timestamptz not null default now()
);

-- Append-only, and written ONLY when a status actually changes.
-- This is what replaces a per-minute snapshot: roughly 1000x smaller for the
-- same information, because a component's status rarely changes.
create table if not exists status_events (
    id                bigserial primary key,
    service_id        bigint      not null references services (id) on delete cascade,
    component_id      bigint      references components (id) on delete cascade,
    status            text        not null,
    changed_at        timestamptz not null default now(),
    source_changed_at timestamptz,
    incident_id       text,
    created_at        timestamptz not null default now()
);

create index if not exists status_events_service_idx on status_events (service_id, changed_at desc);
create index if not exists status_events_component_idx on status_events (component_id, changed_at desc);

create table if not exists incidents (
    id          bigserial primary key,
    service_id  bigint      not null references services (id) on delete cascade,
    source_id   text        not null,
    name        text        not null,
    status      text        not null,
    impact      text,
    started_at  timestamptz,
    resolved_at timestamptz,
    url         text,
    created_at  timestamptz not null default now(),
    updated_at  timestamptz not null default now(),
    unique (service_id, source_id)
);

create index if not exists incidents_service_idx on incidents (service_id, started_at desc);

-- Per-vendor parser health. Used to detect drift and breakage: a vendor that
-- stops parsing, or starts using status vocabulary we do not recognise, shows
-- up here instead of silently reporting "ok".
create table if not exists poll_runs (
    id               bigserial primary key,
    service_id       bigint      not null references services (id) on delete cascade,
    ok               boolean     not null,
    http_status      integer,
    components_found integer     not null default 0,
    unknown_statuses text[]      not null default '{}',
    error            text,
    duration_ms      integer,
    created_at       timestamptz not null default now()
);

create index if not exists poll_runs_service_idx on poll_runs (service_id, created_at desc);

-- Daily aggregation derived from status_events. Filled in later by a rollup
-- worker; the table exists now so the schema does not have to change for it.
create table if not exists daily_rollups (
    component_id        bigint      not null references components (id) on delete cascade,
    day                 date        not null,
    ok_seconds          integer     not null default 0,
    degraded_seconds    integer     not null default 0,
    partial_seconds     integer     not null default 0,
    major_seconds       integer     not null default 0,
    maintenance_seconds integer     not null default 0,
    unknown_seconds     integer     not null default 0,
    incident_count      integer     not null default 0,
    updated_at          timestamptz not null default now(),
    primary key (component_id, day)
);
