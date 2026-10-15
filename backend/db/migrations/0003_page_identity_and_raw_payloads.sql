-- 0003_page_identity_and_raw_payloads.sql
--
-- Two additions that both come from the same incident: a vendor was briefly
-- pointed at another vendor's status page and we stored its data under the
-- wrong name without noticing.
--
-- page_id pins a vendor to the status page it resolved to on its first poll.
-- If a later poll reports a different page, or reports a page another vendor
-- already owns, the write is refused instead of silently corrupting the data.
--
-- raw_payloads keeps the raw response, gzipped, so a parser fix can be
-- replayed over history. Without it, fixing an adapter only affects the future.

alter table services add column if not exists page_id text;
alter table services add column if not exists allow_shared_page boolean not null default false;

create index if not exists services_page_id_idx on services (page_id) where page_id is not null;

create table if not exists raw_payloads (
    id          bigserial primary key,
    service_id  bigint      not null references services (id) on delete cascade,
    fetched_at  timestamptz not null,
    http_status integer,
    body        bytea       not null,
    created_at  timestamptz not null default now()
);

create index if not exists raw_payloads_service_idx on raw_payloads (service_id, fetched_at desc);
create index if not exists raw_payloads_fetched_idx on raw_payloads (fetched_at);
