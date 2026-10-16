-- 0009_disabled_users.sql
--
-- An account can be switched off without being deleted. Deleting a user takes
-- their private services with it, because owner_id cascades, so disabling is the
-- reversible option an admin should reach for first.
--
-- A disabled account cannot sign in, and any session it already had stops
-- resolving, so switching it off takes effect at once.

alter table users add column if not exists disabled_at timestamptz;
