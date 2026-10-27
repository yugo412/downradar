-- 0023_delete_account.sql
--
-- An account can now delete itself. What belongs to the person goes with it,
-- while what belongs to the catalogue stays behind.
--
-- An alert subscription is theirs: it points at the address or webhook they
-- gave, and it exists because they asked for it. Deleting the account ends the
-- subscription and the channels, targets and deliveries under it. A
-- subscription made without an account has no user_id, so it is untouched and
-- keeps the unsubscribe link in its messages.
--
-- A service is not theirs in the same sense. owner_id is attribution, not
-- ownership: the row stays in the catalogue and only the name attached to it is
-- cleared. This is the opposite of the original rule, which took a deleted
-- owner's private services with them.

alter table alert_subscriptions
  drop constraint alert_subscriptions_user_id_fkey,
  add constraint alert_subscriptions_user_id_fkey
    foreign key (user_id) references users (id) on delete cascade;

alter table services
  drop constraint services_owner_id_fkey,
  add constraint services_owner_id_fkey
    foreign key (owner_id) references users (id) on delete set null;
