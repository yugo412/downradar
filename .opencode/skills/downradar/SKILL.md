---
name: DownRadar
description: How to work in this repository. Language and comment rules, the issue to pull request flow and the shape of what it produces, the product rules that constrain a change, and how to run and check work in the dev stack. Load it before starting anything here.
---

# Working in this repository

Load this before starting a change and follow it over whatever habit you arrived
with. It exists because the maintainer has had to say each of these more than once.

## What the project is

downradar.info is a public status aggregator. A Go backend reads the official
status pages of vendors, stores what it finds and serves it over a JSON API, and a
Nuxt frontend renders it. Vendors, services and accounts are managed through the
app itself, not from fixtures.

## Language

- Go comments are used sparingly and are in English. Logs and errors are English.
- Config files (YAML, compose, Makefile, SQL, Dockerfiles) and the README may carry
  English comments and prose.
- Everything a visitor can read is English: labels, empty states, emails, pages.
- You talk to the maintainer in Indonesian. Everything committed or published is
  English.

## Issues, branches, commits, pull requests

- One issue per change: `gh issue create`, then `gh issue develop <n> --base main
  --name <n>-<slug> --checkout`, then implement.
- Branch off `main`, unless the maintainer asks for a branch that stacks on the
  current work, in which case base it on that branch.
- Give the issue a label that matches what it describes, and the pull request the
  same label: `documentation` for documentation, a skill or other repository
  tooling, `bug` for something broken, `enhancement` for a new feature.
- Commits are a single subject line. No body, no `Closes #NN`.
- Issue and pull request bodies are one or two short paragraphs. No bullet lists,
  no code blocks.
- Never write the issue number in a pull request body.
- Open the pull request as a draft with `gh pr create --draft`. Run `gh pr ready`
  only when asked.
- Do not commit until the maintainer says the work is fine. Leave the change in the
  working tree, report it, and wait.
- Do not inspect pull request state on GitHub: no `gh pr view`, `gh pr checks`,
  `gh pr list`, `gh pr run`. Do not summarise pull request status, and do not
  advise on merge order.

## Product rules

- Vendors are added through the form in the app. `seed/vendors.yaml` is not the way
  a new one gets in.
- Production deploys natively. There is no Docker in production.
- The UI does not truncate text by hand, and the vendor index does not use infinite
  scroll.
- `/services` is the service surface: any signed in account may add a service and
  see the ones it added there, while admin and moderator see the whole catalogue.
  A subscriber never picks an adapter, so their row is saved as no match and the
  detection pass publishes it once a profile reads its page. The owner edits the
  fields of their own row, while the adapter, its endpoint, the stage and the
  detection queue stay with staff.
- Visibility and ownership are separate answers. `visibility` says who may see a
  row, `owner_id` says who added it and looks after it, so a catalogue service
  can still belong to somebody. A subscriber's suggestion keeps its owner when it
  is approved.
- Per-user limits live in `user_limits` and are written when the account is made;
  that row is what is read. The alert limit is the only one so far, and it counts
  vendors rather than the things watched inside one: a guest gets one active alert
  and it is a whole vendor, staff are uncapped.
- Alert subscriptions go by email, webhook or Telegram, and watch a vendor's
  overall status, one of its components, or several things at once. Components are for subscribers, so a
  signed out visitor is only ever offered the whole vendor. They fire on the
  transition of whatever is watched, and delivery is an outbox row written in the
  same transaction as the change and drained by `internal/notify`.
- With no SMTP configured, register, forgot-password, sign in link, contact and
  vendor-alert posts answer 503 instead of accepting something they cannot send.
- Signing in is by a link mailed to the address, which works once, expires in
  fifteen minutes and confirms the address it was sent to. The password form sits
  behind a link of its own and has a remember me that decides whether the cookie
  outlives the browser. A sign in link is only sent to an address that already
  has an account.
- Every email is HTML written from a template in `internal/mail/templates`, set in
  one shell that carries the brand and the site's colours. The wording of a
  message belongs in its template, not in the package that sends it.
- Error messages that reach a user start lower case.
- A page that refuses a poll is not asked again until its wait is over. A 429 or
  503 is honoured through `Retry-After` when it carries one, otherwise the wait
  doubles with each refusal in a row; a 403 goes on a slow schedule. No vendor is
  polled more often than once a minute, and vendors do not share a due second.
- Both themes are custom daisyUI theme blocks in `frontend/app/assets/css/main.css`
  with the built-ins switched off. They share one palette, and the state colours
  carry white text. `.badge-picked` is a hand written rule because daisyUI's badge
  rule sits in a nested layer that outranks a plain Tailwind utility.

## Working with the maintainer

- "Why is this code like this" wants the reasoning, not a demonstration. Do not run
  anything to prove a point.
- Design calls are the maintainer's. Make the change that was asked for, say what it
  costs elsewhere, and offer the follow-up rather than shipping unrequested polish.
- Report state plainly: files touched, values chosen, commands run, what passed.
  Short, and in Indonesian.

## The dev stack

- `docker compose -f compose.dev.yml up` brings up api on 8080 (air), web on 3000,
  postgres on 5433 and mailpit on 8026.
- Restart the api after a change to `services.yaml` or to code. A `.env` change
  needs `make reload`.
- Backend tests run on the host against the dev database, with
  `DATABASE_URL="postgres://downradar:downradar@localhost:5433/downradar?sslmode=disable"`.
- The frontend has no lint or test tooling. CI builds it with `npx nuxt build`, and
  the host's `node_modules` is unusable, so build inside the container instead.
- A new `@plugin` block in `frontend/app/assets/css/main.css` is not picked up until
  the web container is restarted.
- For a visual check, drive headless Chromium through the `playwright-core` in the
  yarn cache and the browser under `~/.cache/ms-playwright`. Tailwind only emits the
  classes it finds in source, so a variant has to live in a `.vue` file to be seen.
