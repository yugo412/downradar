-- 0024_generated_summaries.sql
-- A vendor gets one short summary when its state changes. The summary is display
-- copy written from the same structured state the page already shows; it is
-- never the source of truth, and a healthy vendor has no row here.

create table if not exists service_summaries (
    service_id   bigint      primary key references services(id) on delete cascade,
    summary      text        not null,
    model        text        not null default '',
    input_hash   text        not null,
    generated_at timestamptz not null,
    created_at   timestamptz not null default now(),
    updated_at   timestamptz not null default now()
);
