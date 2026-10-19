-- 0016_alert_service.sql
--
-- A subscription is about one vendor, and that is worth saying outright. It was
-- implied by its targets, which was enough while an email was the only way an
-- alert arrived and the targets were therefore the whole of what it watched. A
-- webhook watches the vendor rather than a pick of its components, because its
-- body carries the vendor's whole state, so a subscription with only a webhook
-- would have had no row to hang that on.
--
-- What is left is two different questions. The subscription says which vendor
-- somebody is watching. Its targets say what the email way reports on: the
-- vendor's own status, the components of it that were picked, or both. A webhook
-- is written a delivery for every change of the vendor, whatever the targets
-- say, so a delivery now names what changed itself rather than pointing at a
-- target an email is watching.

alter table alert_subscriptions
    add column service_id bigint references services (id) on delete cascade;

-- Every subscription that exists was made on one vendor, which is the service
-- its targets point at. They all point at the same one: a save writes the set
-- for one vendor at a time.
update alert_subscriptions a
set service_id = t.service_id
from (select distinct on (subscription_id) subscription_id, service_id
      from alert_targets
      order by subscription_id, id) t
where t.subscription_id = a.id;

-- A subscription with no targets watched nothing and could not have been sent
-- anything, which is a save that asked for nothing. There is nothing to keep.
delete from alert_subscriptions where service_id is null;

alter table alert_subscriptions
    alter column service_id set not null;

-- One account watches one vendor once, and this is what makes that true rather
-- than merely intended. A pair can still be there from an older build: changing
-- an address used to look for the alert by address, which found nothing and
-- made a second one beside the first. They are one alert to the person who is
-- sent both, so the older row keeps everything and the newer one goes.
with pair as (
    select id, user_id, service_id,
           first_value(id) over (partition by user_id, service_id order by id) as keep
    from alert_subscriptions
    where user_id is not null
)
insert into alert_channels (subscription_id, kind, address, settings, token,
                            paused_at, unsubscribed_at, created_at, updated_at)
select p.keep, c.kind, c.address, c.settings, c.token,
       c.paused_at, c.unsubscribed_at, c.created_at, c.updated_at
from alert_channels c
join pair p on p.id = c.subscription_id and p.id <> p.keep
on conflict (subscription_id, kind, address) do nothing;

with pair as (
    select id, user_id, service_id,
           first_value(id) over (partition by user_id, service_id order by id) as keep
    from alert_subscriptions
    where user_id is not null
)
insert into alert_targets (subscription_id, service_id, component_id, created_at)
select p.keep, t.service_id, t.component_id, t.created_at
from alert_targets t
join pair p on p.id = t.subscription_id and p.id <> p.keep
on conflict do nothing;

with pair as (
    select id, user_id, service_id,
           first_value(id) over (partition by user_id, service_id order by id) as keep
    from alert_subscriptions
    where user_id is not null
)
delete from alert_subscriptions a
using pair p
where a.id = p.id and p.id <> p.keep;

-- Two email channels can survive that merge, when the pair had different
-- addresses. A save writes one of each kind, so the next one cleans that up.
create unique index alert_subscriptions_user_service_uniq
    on alert_subscriptions (service_id, user_id) where user_id is not null;

create index alert_subscriptions_service_idx on alert_subscriptions (service_id);

-- The enqueue reads the target list of one subscription to decide which emails a
-- change earns, and the unique indexes on alert_targets already lead on
-- subscription_id. The two that led on the watched thing were for the old
-- question, which is now asked of the subscription itself.
drop index if exists alert_targets_service_idx;
drop index if exists alert_targets_component_idx;

alter table alert_deliveries
    add column service_id   bigint references services (id) on delete cascade,
    add column component_id bigint references components (id) on delete cascade;

-- The target a delivery points at knows what changed, so it is copied across
-- before the pointer goes. A null component is the vendor's own status, which is
-- what the target was when it had no component either.
update alert_deliveries d
set service_id = t.service_id, component_id = t.component_id
from alert_targets t
where t.id = d.target_id;

-- A delivery whose target is gone went with its foreign key already, so this
-- only guards the constraint below.
delete from alert_deliveries where service_id is null;

alter table alert_deliveries
    alter column service_id set not null,
    drop column target_id;
