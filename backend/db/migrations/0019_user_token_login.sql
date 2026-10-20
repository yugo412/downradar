-- 0019_user_token_login.sql
--
-- Signing in can now be done from a link in an inbox rather than a password, so
-- user_tokens carries a third kind. The check is replaced rather than dropped:
-- the column is still only one of the links the code knows how to make, and the
-- kinds stay apart so a confirmation cannot be spent as a way in.

alter table user_tokens drop constraint if exists user_tokens_kind_check;
alter table user_tokens add constraint user_tokens_kind_check
  check (kind in ('verify', 'reset', 'login'));
