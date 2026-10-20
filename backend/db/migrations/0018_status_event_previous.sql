-- The status a change moved from, so a feed item can say where it came from
-- rather than only where it landed.
alter table status_events add column if not exists previous_status text;

-- The history already says what each change moved from: it is the status the same
-- target was last recorded in, which is the same thing the writer reads before it
-- writes a change. Filling it here means a feed carries where changes came from
-- from the moment it is published rather than only for changes that happen after.
-- The first state a target was ever seen in has nothing before it and is left
-- null, which a feed tells as a state it is in rather than as a move.
update status_events e
set previous_status = p.previous
from (
    select id,
           lag(status) over (partition by service_id, component_id order by changed_at, id) as previous
    from status_events
) p
where p.id = e.id and p.previous is not null and e.previous_status is null;
