# downradar.info

Aggregated status of popular services, taken directly from their **official**
status pages instead of crowdsourced reports.

## How it works

```
                 ┌──────────────────────────────┐
  visitor ─────► │  Nuxt (SSR, :3000)           │
                 └───────────┬──────────────────┘
                             │ /api/** proxied
                             ▼
                 ┌──────────────────────────────┐
                 │  Go API + poller (:8080)     │
                 └───────────┬──────────────────┘
                             ▼
                      PostgreSQL
```

- **Go** polls vendors, normalises the results and owns the database. It knows
  nothing about HTML.
- **Nuxt** renders pages and has no database access. It only calls the Go API.

## Requirements

- Docker + Docker Compose
- Nothing else. Go and Node only run inside containers.

## Getting started

```bash
cp .env.example .env     # then edit it, see below
make dev                 # build and run everything in the foreground
```

`.env` holds the database credentials, the shared secret between the frontend
and the API, the rate limits, and optional provider credentials. It is gitignored,
so only `.env.example` is committed. Generate a real secret with:

```bash
openssl rand -hex 32
```

After editing `.env`, run `make reload`. A plain `docker compose restart` reuses
the existing container environment and keeps the old values.

Then open:

| URL | What |
| --- | --- |
| http://localhost:3000 | the site |
| http://localhost:8080/api/v1/vendors | the API |
| http://localhost:3000/api/v1/mcp | the MCP endpoint |
| http://localhost:3000/api | API documentation |
| http://localhost:8026 | Mailpit (captures outgoing email) |

Other useful targets:

```bash
make help       # list every target
make logs-api   # follow backend logs
make psql       # open psql
make sql Q="select slug, status from services s join service_state ss on ss.service_id = s.id limit 10"
make test       # run backend tests inside the container
make reset      # stop everything and delete all data
```

## Deployment

`make deploy` builds the api and web images on this machine, pushes them to
`ghcr.io/yugo412`, and rolls the host to them. CI runs the same `deploy/ship.sh`
on every merge to main, tagging the images with the commit, so the site follows
main rather than a release. Nothing is built on the server: it has 1.9 GB of RAM
and already serves other sites, so a Nuxt build there risks the OOM killer
taking one of them down.

A `v*` tag does not deploy. The release workflow re-publishes the image already
built for that commit under the version name and opens a GitHub release, so the
published version and the running site are the same bits.

The machine runs Caddy and the Docker daemon. Caddy terminates TLS and forwards
to the web container, whose published port is on the loopback interface. The host
keeps only:

```
/srv/downradar/
  compose.yml     copied from compose.prod.yml on every deploy
  services.yaml   the vendor registry, bind mounted read only into the api
  seed/           the bootstrap vendors, read only, used by the seed command
  .env            secrets and addresses, never overwritten by a deploy
```

The stack is `db`, `redis`, `api` and `web`. Postgres keeps its data in the
`db-data` volume and Redis in `redis-data`; both are dialled by service name
(`db:5432`, `redis:6379`). The web server reaches the api as `http://api:8080`
instead of the host loopback. Deploys are named by commit, so rolling back is
dispatching the deploy workflow with an earlier tag; the image is shipped as it
is, without a rebuild:

```bash
gh workflow run deploy.yml -f image_tag=<earlier sha>
```

The seed and user commands ride in the api image:

```bash
cd /srv/downradar
sudo docker compose run --rm api downradar-seed -file /app/seed/vendors.yaml
sudo docker compose run --rm api downradar-user -email you@example.com -name "Your Name" -role admin
```

`services.yaml` is bind mounted rather than baked in, so updating a profile is a
file copy and `docker compose restart api`, with no release behind it.

Alerts need `SMTP_ADDR`, `MAIL_FROM`, `PUBLIC_URL` and `APP_KEY` in `.env`.
`PUBLIC_URL` has to be the public address, or every unsubscribe link points at
localhost. `APP_KEY` seals the secrets kept in alert channel settings; generate
it with `openssl rand -hex 32` and keep it with the rest of `.env`, because
losing it makes saved webhook headers, bot tokens and Lark signing secrets unreadable. Without
`SMTP_ADDR` the API starts with alerts off and refuses subscriptions rather than
accepting them and sending nothing. The relay must accept the host's address; a
relay that filters by IP rejects the SMTP login with a plain authentication
failure. `TELEGRAM_API_BASE` points the Bot API at a server of your own; the bots
themselves belong to the subscribers, so no bot token is set here.

One-click Slack setup needs `SLACK_CLIENT_ID` and `SLACK_CLIENT_SECRET` from an
app at api.slack.com/apps, with the incoming-webhook scope and
`PUBLIC_URL/api/v1/slack/callback` registered as its redirect URL, and the app
distributed. A plain HTTP redirect such as the dev site's blocks distribution
until it is removed. The signing secret is unused because the site does not take
Slack events. Without the two values the button is off and the manual webhook
field is still there.

One-click Discord setup needs `DISCORD_CLIENT_ID` and `DISCORD_CLIENT_SECRET`
from an application at discord.com/developers/applications, with the
`webhook.incoming` scope and `PUBLIC_URL/api/v1/discord/callback` registered as
its redirect URL. It does not install a bot: the install returns the channel's
incoming webhook. Without the two values the button is off and the manual webhook
field is still there.

Generated summaries need `OPENAI_API_KEY` in `.env`; the key is never committed.
When it is empty no new sentence is written, and rows already stored are left
alone. `SUMMARY_ENABLED` defaults to true and can turn the worker off even while
a key is set; stored sentences are then hidden but kept for when it is turned
back on. `OPENAI_MODEL` defaults to `gpt-6-luna`,
and `OPENAI_BASE_URL` can point at a compatible endpoint. A vendor's sentence is
written after a successful poll when the stored state changed, then read from the
database by the page and by alert emails. A healthy vendor keeps no sentence, and
a model failure leaves the page with the official lists rather than a summary
that describes the wrong state.

Things that are easy to get wrong:

- **Never read secrets in `nuxt.config.ts`.** Nuxt inlines them into the bundle at
  build time, so the web image is built without secrets and everything comes from
  `NUXT_*` variables at runtime. `NUXT_PAGE_CACHE_SECONDS` is the one build-time
  value, because it sets the route rules. Both `make deploy` and CI refuse an
  image whose bundle contains the internal token.
- **The api's `INTERNAL_TOKEN` and the web's `NUXT_INTERNAL_TOKEN` must match.**
  `compose.prod.yml` gives both containers the same value from `.env`. Setting
  only one makes every proxied request look like a stranger and the site quietly
  rate limits itself.
- **The image must be reachable from both ends.** `make deploy` pushes to
  `ghcr.io/yugo412`, so it needs a `docker login ghcr.io` for the account that
  owns the packages. The host needs nothing while the packages are public, and a
  read only token when they are not.
- **The API must see the real client IP.** Caddy sets `X-Forwarded-For`
  authoritatively and the Nitro proxy passes it through; otherwise every visitor
  shares one rate limit bucket. Behind Cloudflare, change that Caddy directive to
  `{http.request.header.CF-Connecting-IP}`.

## Adding a vendor

A vendor is a row in the `services` table, not an entry in a file. New vendors
are added through the form in the app; `services.yaml` only says how a platform
is polled. The initial catalogue comes from `seed/vendors.yaml`, read once by a
separate command:

```bash
cd backend
go run ./cmd/seed -file seed/vendors.yaml -dry-run   # validate only
go run ./cmd/seed -file seed/vendors.yaml            # write
```

The seed only upserts, so a vendor added later is never deactivated by a reseed,
and editing the file afterwards has no effect.

Each row names a `profile` from `services.yaml`, which decides the adapter, the
endpoint, the default intervals and the one-line description the management
screens show. A row can override the endpoint and intervals for itself.

A profile can be marked `visible: false` for a page written for one vendor rather
than a platform. It is then left out of the default profile picker, which stays a
list of platforms; staff can still assign it from the single-vendor section of
the service form. Detection never tries it.

Validation happens on the write path: an unknown profile, duplicate slug or
internal url is rejected before a row exists. The poller trusts the table and
skips, with a warning, any row naming a profile that is no longer declared.

A vendor added without a profile goes to the detect queue: the profiles listed
under `detect` in `services.yaml` are tried in order, and the first adapter that
can read the page wins. The url alone is enough, so its platform does not have to
be known first. A service with no status page does not belong in the queue:
`ping` is picked by hand for it, since in the queue it would answer for any url
that is up and take every page no other profile could read.

## How a vendor is published

The suggest form writes a row with `status = 'proposed'`. A proposed row is not
polled and has no public page. A moderator opens `/services`, filters to Proposed,
and fills in the url and profile. Leaving the profile empty runs the detect queue
instead of refusing the row, and the add form has a Detect button that fills the
picker before anything is saved. A signed in suggester is stored as the row's
owner as attribution, not visibility; approving publishes it and keeps that owner.

When nothing in the queue can read the page, the vendor is saved as `no_match`:
the name and url are kept, the poller leaves it out of the rotation, and it is
offered to the queue again once an hour. Every attempt is written down, so a
parked vendor's fetch log still shows what each profile made of the page. Writing
a profile for its platform turns it into a polled vendor on the next hourly pass.
Retry on a No match row runs the queue immediately, so a freshly deployed profile
does not have to be waited on.

Deleting a proposal removes the row outright: it has no history worth keeping.
Deleting a published vendor only deactivates it, so its history survives and it
can be restored from the Removed filter.

## Alerts

A signed out visitor can ask for an email alert on a whole vendor from the bell.
Signed in subscribers get the same bell plus a page per service and `/alerts`,
where they choose what to watch and how it arrives.

- **Ways:** email, webhook, Telegram, Slack, Discord, Lark, or a mix. Each is set on the page; an empty field turns it off, and one Save writes the whole alert.
- **Targets:** email, Telegram, Slack, Discord and Lark report on what is ticked: the vendor's status, components, or both. A webhook always carries the whole vendor. A change in either direction is sent; a first sighting is a baseline, not a change.
- **Pause:** each way has a switch in its row in the way list. Pausing keeps its address, URL or chat and stops only that way. `/alerts` can pause, resume or delete the whole subscription. A pending delivery for a paused or ended way is dropped.
- **Limits:** signed out gets one alert, tied to the address. A subscriber uses the limit in `user_limits`, counted per vendor rather than per target; staff are uncapped. The alert address need not be the account's.
- **Channels and tokens:** each channel has its own token, so an unsubscribe link stops only the channel it came from. An alert holds at most one of each way.
- **Email:** every message ends with an unsubscribe link. It opens a confirmation page first, because mail clients and scanners fetch links.
- **Webhook:** the subscriber sets the URL, method and headers; the body is fixed JSON with the vendor, the change, the time, a ready-to-send sentence and every component. Statuses use stored values such as `major`.
- **Webhook methods:** POST, PUT and PATCH only; GET is refused. Header values can use `{{service}}`, `{{slug}}`, `{{component}}`, `{{from}}`, `{{to}}`, `{{at}}`, `{{url}}` and `{{text}}`; unknown placeholders stay as written, and a value cannot contain a line break.
- **Webhook auth:** Standard Webhooks is the default and signs every call. The secret is shown once; a rotation keeps the old one valid for a day. Older webhooks keep custom headers, and subscriber headers are sent either way.
- **Webhook test:** the page can send a made-up change with the current URL, method and headers. The last fifty calls are kept, without the sent body or header list, and the log shows twenty at a time.
- **Telegram:** use the subscriber's own bot from BotFather, press Start, then enter the chat id from @userinfobot. The token stays masked and can be revealed, tested or replaced.
- **Slack:** use an incoming webhook URL, or Add to Slack for the site's app. Messages use Block Kit; the URL stays masked, the channel name is shown, and the channel can be tested before saving.
- **Discord:** use an incoming webhook URL, or Add to Discord for the site's app. The post is an embed with the status, a link to the vendor page and a link that stops that channel.
- **Lark:** use a custom bot's webhook from the group, plus its signing secret when signature verification is on. The card carries the status, a link to the vendor page and a link that stops the channel. The page shows the last real delivery result; tests do not replace it.
- **Delivery:** a status change writes one delivery per channel in the same transaction; a loop drains them and retries failures. A slow endpoint cannot hold up polling, and a restart loses nothing.
- **SMTP:** alerts are off without `SMTP_ADDR`, and the API refuses subscriptions while off. Port 465 uses TLS from the first byte; other ports use STARTTLS when offered. Email wording lives in `internal/mail/templates`.

## Feeds

Every status change is also published as a feed: `/rss.xml` and `/rss.json` for
the site, and `/{slug}/rss.xml` and `/{slug}/rss.json` for a service. The XML is
RSS and the JSON is JSON Feed, with the same items. An item says the vendor, the
component or whole service, the statuses it moved between and when; its id is the
status event's own, so a reader does not show a change twice. The first state a
target is seen in is not carried.

Feeds are views of the history the pages already keep: nothing is stored and
there is nothing to unsubscribe from. They are served by the site from routes
that read the API the way a page does, and are cached for a minute. The alert page
offers both shapes with a copy button.

## MCP

The catalogue is also a read-only Model Context Protocol server at
`/api/v1/mcp`, mounted beside the JSON API as a stateless Streamable HTTP
endpoint. It offers `list_vendors`, `search_vendors`, `get_vendor` and
`recent_changes`; every tool reads the same public store methods as the API.
There is no account, management or write path behind it, and its requests share
the read rate limit, counted per caller even through the Nuxt proxy.

## Private services

`visibility = 'private'` keeps a row out of the public list, vendor pages, stats
and sitemap; only its owner and staff can see it. It is a separate answer from
ownership: `owner_id` says who added and looks after a row, so a catalogue service
can still belong to somebody. `status` says whether it is polled.

`/services` is the service surface: any signed in account can add a service and
see the ones it owns, while staff see the whole catalogue. A subscriber's row is
saved as `no_match` because they do not pick an adapter; staff keep the address,
adapter, endpoint, stage and detection queue. The owner edits the other fields,
and staff can move a service between public and private. A private service has no
public page, so its detail lives at `/services/{slug}`, excluded from the page
cache. It also appears on the home page for its owner, marked `private`; that part
is fetched and merged in the browser so the cached home page stays identical for
everybody.

## Accounts

Registering always makes a subscriber. An address on the site's own domain is
refused with an answer that says no more than that the account could not be made.
Registering signs the person in and sends a link that confirms the address; the
banner offers to resend it. Nothing is blocked while an address is unconfirmed.

Signing in does not need the password: `/login` asks for an address and mails a
one-use link that expires in fifteen minutes. Following it also confirms the
address it was sent to. An address with no account is refused and gets no mail, so
the form cannot be used to write to strangers; asking for a link has its own
tighter rate limit. The password form is one click away and has a remember me;
without it the session cookie is dropped when the browser closes.

A forgotten password is reset by email with a one-use link that expires in an
hour and ends every session. Every kind of link is stored hashed, a new one
retires the previous one, and the kinds are kept apart so a confirmation link
cannot be spent as a way in.

## Signing in with a provider

`/login` and `/register` offer GitHub and Google when the deployment has the
credentials. A button sends the browser to `/api/v1/auth/{provider}` with a state
in a short-lived cookie. The provider returns to
`/api/v1/auth/{provider}/callback` with a code, the API trades it for the account,
and the browser gets the same session cookie the password form issues.

The account is found by provider id first, then by verified address, and only
made when neither exists. An existing address links the provider to that account.
A new account is a confirmed subscriber with its limits row; a disabled account
is refused, and a site-domain address is not made into a new account.

The credentials are `GITHUB_CLIENT_ID` and `GITHUB_CLIENT_SECRET`, and
`GOOGLE_CLIENT_ID` and `GOOGLE_CLIENT_SECRET`. The registered callback is
`PUBLIC_URL` plus `/api/v1/auth/github/callback` or
`/api/v1/auth/google/callback`. With either half of a pair empty the button is not
offered and its routes say the provider is not configured. Links live in
`user_providers`, one row per provider per account, so another provider is another
implementation of the interface in `internal/oauth`.

## Contact form

`/contact` asks for a name, address, subject and message, all required; a signed
in visitor gets the first two filled in. The message is mailed to
`CONTACT_EMAIL`, with the reply address set to the sender, and the subject is
prefixed so it stands out among alert and account mail.

Sending is not queued: the form waits for the mail server because the person is
waiting to be told it went. It is refused while `SMTP_ADDR` is unset and has its
own rate limit so one visitor cannot fill the inbox.

## Staff sign in

Staff sign in is not advertised. The form is at `/login`, and an administrator is
still made from the shell:

```bash
cd backend
go run ./cmd/user -email you@example.com -name "Your Name" -role admin
go run ./cmd/user -email you@example.com -reset          # new password
```

With no `-password` the command generates one and prints it once. `-role` takes
`admin`, `moderator` or `subscriber`; an account made this way is already
confirmed.

| Role | Can do |
| --- | --- |
| `admin` | everything, including removing a published service |
| `moderator` | add, edit, publish, and delete a proposal; cannot remove a published service |
| `subscriber` | get alerts, add services and look after the ones they own; staff see the whole catalogue |

Sessions live in an httpOnly, `SameSite=Lax` cookie, and only the hash of the
token is stored, so a database dump does not hand out working sessions. A password
reset ends every session the old password opened.

Everyone signed in gets a menu separate from the Service one:
`/account/profile` changes the display name and address, and `/account/password`
changes the password. Changing the address sends a fresh confirmation link.
Changing a password signs out every other device but keeps the current one. Both
endpoints ask only for a session, not a role.

The management screens render on the client only, so a session never ends up in
cached HTML. `server/middleware/session-guard.ts` rejects an unauthorised request
before anything is rendered, and the API checks the role again on every call.

## Architecture notes

- **The poller reads the vendor list from the table every cycle.** A row written
  by a form takes effect on the next cycle, with no file watcher in between.
- **History is stored as changes, not snapshots.** `status_events` only gets a row
  when a status changes; per-minute snapshots would cost about 1000x more storage.
- **Polling is adaptive.** 300s while healthy, 60s while degraded, and never more
  often than once a minute. A slug-derived share of the interval keeps vendors
  from falling due on the same second.
- **A refusing page is left alone.** A 429 or 503 is honoured through
  `Retry-After`, or a wait that doubles with each refusal up to six hours. A 403,
  which suggests a datacenter address, puts the vendor on an hourly schedule. The
  first successful poll clears the wait.
- **Empty responses never become "ok".** Unknown vocabulary maps to `unknown` and
  is recorded in `poll_runs.unknown_statuses` so drift is visible.
- **The fetch log is staff and owner only.** A vendor's public page and its
  `/services/{slug}` row list recent `poll_runs`, including detection attempts. A
  failed fetch changes no status, so this is the only place a page that stopped
  answering shows up. Staff also see when a refused page will be tried again. On
  the public page the log is rendered in the browser, not into cached HTML.
- **`/api/**` is proxied by Nuxt**, so the browser and SSR use the same relative
  path and no CORS is involved.

## Layout

```
services.yaml             adapter profiles: kind, endpoint, intervals, visibility

seed/vendors.yaml         bootstrap vendors, read once by cmd/seed

backend/                  Go: poller, adapters, store, API
  cmd/server/             entrypoint
  cmd/seed/               imports the bootstrap file into the table
  cmd/user/               creates an account, or resets its password
  internal/adapters/      the adapter contract and helpers, one package per platform
  internal/adapters/all/  imports every adapter package, so each registers itself
  internal/auth/          roles, bcrypt, session tokens
  internal/catalog/       services.yaml -> profiles, with validation
  internal/mail/          SMTP, and the HTML templates every email is written from
  internal/mcp/           read-only Model Context Protocol server
  internal/seed/          seed/vendors.yaml -> targets, with validation
  internal/poller/        reads the table each cycle, scheduling, worker pool
  internal/store/         the only package that speaks SQL
  internal/summary/       writes and stores the sentence above a vendor's incidents
  db/migrations/          embedded .sql migrations

frontend/                 Nuxt 4 + Vue 3 + TypeScript + DaisyUI
  app/pages/              index, per-vendor, login, account, and the service screens
  app/components/         StatusBadge, the status view, the service form and its dialogs
  server/middleware/      the api proxy, and the session guard on /services and /account

compose.dev.yml           db, mailpit, api, web
```

`services.yaml` is at the root because it is the file a contributor is most likely
to touch, and it holds only the profiles: what the code can read, not which
vendors are monitored. Vendors live in the table, which is why adding one is a row
rather than a commit.

## Data sources

Everything is read from what the platform publishes, one adapter per platform:
Atlassian Statuspage, Status.io, Sorry, cron-job.org, FlashDuty, PagerDuty, Slack,
Better Stack, SolarWinds, UptimeRobot and StatusCake, plus bespoke adapters for
pages that are not a platform: OpenCode's own page, whose components are slugs and
whose incidents have generated ids, and the pages of Auth0, PayPal, Railway,
Midtrans, Indodax and Microsoft 365. Platform profiles are also in the detect
queue, so a vendor can be added before its platform is known; a page of a
vendor's own kind is chosen by hand, since its profile can only match that one
page. Ping is the profile to pick by hand for a service with no status page: it
watches the target by asking for it, and a request that stops coming back is the
outage. Further platforms are additional adapters; the interface already
separates "how to fetch" from "how to interpret".

Please respect each provider's `robots.txt` and terms. Attribution and a link back
to the official status page are shown on every vendor page.
