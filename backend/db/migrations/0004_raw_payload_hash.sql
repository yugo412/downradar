-- 0004_raw_payload_hash.sql
--
-- raw_payloads was storing every poll, which works out to roughly 200 MB a day
-- because a status page response is mostly identical from one poll to the next.
-- Storing only the payloads that actually differ keeps the replay ability, since
-- a gap in the timeline means "identical to the row before it", and cuts the
-- volume by more than 99%.

alter table raw_payloads add column if not exists body_hash text not null default '';
