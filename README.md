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

- **Go** is the data plane: it polls vendors, normalises the results and owns
  the database. It knows nothing about HTML.
- **Nuxt** is the presentation plane: it renders pages and has no database
  access. It only calls the Go API.

## Requirements

- Docker + Docker Compose
- Nothing else. Go and Node only run inside containers.

## Getting started

```bash
cp .env.example .env     # then edit it, see below
make dev                 # build and run everything in the foreground
```

`.env` holds the database credentials, the shared secret between the frontend
and the API, the rate limits, and the optional GitHub token. It is gitignored,
so only `.env.example` is committed. Generate a real secret with:

```bash
openssl rand -hex 32
```

After editing `.env`, run `make reload`. A plain `docker compose restart` reuses
the existing container environment and will silently keep the old values.

Then open:

| URL | What |
| --- | --- |
| http://localhost:3000 | the site |
| http://localhost:8080/api/v1/vendors | the API |
| http://localhost:3000/api | API documentation |
| http://localhost:8026 | Mailpit (captures outgoing email in development) |

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

`make deploy` builds everything here and ships it to the server. Nothing is
built on the server: it has 1.9 GB of RAM and is already serving other
production sites, so a Nuxt build there would risk the OOM killer taking one of
them down.

The layout follows the convention already used on that machine:

```
/var/www/downradar.info/
  releases/20261015202256/   both Go binaries, services.yaml, the seed file, and the web bundle
  releases/20261015201543/   previous releases, kept for rollback
  current -> releases/20261015202256
  shared/.env                secrets, not touched by a release
```

Releases are immutable and `current` is a symlink, so rolling back is pointing
the symlink at the previous release and restarting:

```bash
ssh ubuntu@43.133.137.16
sudo ln -sfn releases/<previous> /var/www/downradar.info/current
sudo systemctl restart downradar-api downradar-web
```

Services are `downradar-api.service` and `downradar-web.service`, both running
as the `downradar` user with a memory cap. They read `/var/www/downradar.info/shared/.env`
via `EnvironmentFile`.

Alerts need three more variables in `shared/.env`: `SMTP_ADDR` for the mail
server, `MAIL_FROM` for the sender, and `PUBLIC_URL` for the links an email
carries, which has to be the public address or every unsubscribe link points at
localhost. Without `SMTP_ADDR` the API starts with the feature off and refuses
subscriptions instead of taking them and sending nothing.

Two things that are easy to get wrong:

- **Secrets must not be read in `nuxt.config.ts`.** Nuxt inlines those values
  into the bundle at build time, so a build done locally would ship the local
  secret to production. Everything comes from `NUXT_*` variables at runtime
  instead, and the deploy script refuses to ship a bundle that contains one.
- **The API must see the real client IP.** Caddy sets `X-Forwarded-For`
  authoritatively and the Nitro proxy passes it through, otherwise every visitor
  shares a single rate limit bucket. When Cloudflare is put in front, that Caddy
  directive has to change to `{http.request.header.CF-Connecting-IP}`.

## Adding a vendor

A vendor is a row in the `services` table, not an entry in a file. The table is
the source of truth; `services.yaml` only says how a platform is polled.

The initial set comes from a bootstrap file, read once by a separate command:

```bash
cd backend
go run ./cmd/seed -file seed/vendors.yaml -dry-run   # validate only
go run ./cmd/seed -file seed/vendors.yaml            # write
```

It only ever upserts, so a vendor added later is never deactivated by a reseed,
and editing the file afterwards has no effect.

Each row names a `profile` from `services.yaml`, which decides the adapter, the
endpoint, the default intervals and the one line description the management
screens show. A row can override the endpoint and intervals for itself.

A profile can also be marked `visible: false`, which is what a profile written
for one vendor's own page rather than a platform does. It is then left out of the
profile picker when a vendor is added, so that stays a list of platforms, while
the vendor that already uses it still gets it back when it is edited.

Validation moved to the write path with the data: an unknown profile, a
duplicate slug or an internal url is rejected before a row exists. The poller
trusts the table and skips, with a warning, any row naming a profile that is no
longer declared.

A vendor added without a profile is offered to the detect queue: the profiles
listed under `detect` in `services.yaml` are tried in order and the first one
whose adapter can read the page wins. The url alone is enough, so its platform
does not have to be known first. A service with no status page at all does not
belong in the queue: `ping` is picked by hand for it, since in the queue it would
answer for any url that is up and take every page no other profile could read.

## How a vendor is published

The suggest form writes a row with `status = 'proposed'`. A proposed row is not
polled and has no public page. A moderator opens `/services`, filters to
Proposed, and fills in the url and the profile. Leaving the profile empty runs
the detect queue instead of refusing the row, and the add form has a Detect
button that fills the picker in before anything is saved. A signed in visitor who
suggested the service is stored as the row's owner, which is attribution for the
review queue: approving the suggestion clears that owner and publishes the row to
the catalogue.

When nothing in the queue can read the page, the vendor is still saved, as
`no_match`: the name and the url are kept, the poller leaves it out of the
rotation, and it is offered to the queue again once an hour. Every attempt is
written down, so the fetch log of a parked vendor still shows when it was tried
and what each profile made of the page. Writing a profile for its platform is
what turns it into a polled vendor, after which it starts by itself. The hourly
pass is the usual way a new profile reaches the rows waiting for it; Retry on a
row in the No match filter runs the same queue straight away, so a freshly
deployed profile does not have to be waited on.

Deleting a proposal removes the row outright: it has no history worth keeping.
Deleting a published vendor only deactivates it, so its history survives and it
can be restored from the Removed filter.

## Alerts

A visitor can ask to be emailed when a vendor changes status. The bell on a
vendor page opens a modal that takes an address and lists what the vendor
reports, each component with its latest status, a search box above them and a row
of status badges to narrow the list by: the vendor's own status is one line at
the top, and any of the components can be ticked beside it. No account is needed
for the vendor itself, and a subscription belongs to the vendor it was made on. A
component is a subscriber's, so the bell leaves those boxes alone for a signed
out visitor and points at signing in.

When the visitor is signed in, the bell also reads back what their account
already has here. If there is an alert it shows a checked bell and says
Subscribed, and opening it offers the same list with what is already watched
ticked, so a component can be added or dropped and the whole selection saved in
one go, along with a button to stop the alert. Saving is asking for the emails,
so it starts a paused alert again. An alert made while signed out belongs to the
address rather than to the account, so the bell does not claim it: nothing on the
page can say whether that address is theirs.

Signed in visitors also get a screen at `/alerts`, in the navigation beside the
service menu, listing every subscription their account holds with the address it
goes to, what it watches and what it is doing. From there an alert can be paused, which keeps the
row and stops the email until it is started again, subscribed again after it was
ended, or deleted outright. Anything still waiting for an address that has just
been paused or ended is dropped, so starting it again does not deliver last
week's news. The bell fills the form in with the address of whoever is signed in,
since that is almost always the one they want.

A visitor who is not signed in gets one alert, and that alert is a whole vendor:
a component is a subscriber's, and the API refuses one for a visitor even if a
page were to send it. With no account behind them the address they typed is their
whole identity, so that is what the limit is
counted on, and asking for a second vendor is refused with the name of the vendor
that address already has an alert for, so there is nothing to guess at, along
with a pointer to sign in or to unsubscribe from that one. A subscriber may watch
up to the limit on their own row in `user_limits`, counted per account and per
vendor: a whole service watched together with three of its components takes one
place, not four, and saving over a vendor that is already watched replaces it
rather than adding one. Staff are
not capped. That row starts from `ALERT_LIMIT_PER_SUBSCRIBER` (ten by default) and
is written when the account is made, so one account can differ from another
without touching the environment. The address they give does not have to be the
one on their account. The id of a signed in subscriber is recorded with the row,
so it can be tied to them later without asking again.

An email goes out every time something a subscription watches changes, in either
direction, so a recovery is announced as much as an outage: a vendor's overall
status, or a component that was ticked, and the message says which. The first
status a thing is ever seen in is a baseline rather than a change, so subscribing
to a vendor that has been up for months does not produce a "it is up" email.

What a subscription asks for is already kept apart from the two things around it.
A subscription is only who wants to hear. What it watches is a target: a vendor's
overall status, or one of its components, and a subscription can hold several.
How it arrives is a channel: an address today, a webhook or a chat service later,
each with a token of its own, so the unsubscribe link in a message stops the
channel that message came from and leaves the others alone. Pausing from the
`/alerts` screen is the subscription going quiet, which is every channel it has.
Nothing in the product puts more than one channel on a subscription yet, and the
tables hold the rest so that adding one is a matter of writing it rather than of
reshaping what is already stored.

Each email ends with an unsubscribe link. It opens a page that asks before it
acts, because mail clients and link scanners fetch the links they are sent, and a
link that unsubscribed on a fetch would do it without anyone asking. The link
carries a token, which is the only handle on the subscription: there is no
account behind it and no password to lose.

Sending is an outbox rather than something the poller does inline. A status
change writes one delivery per subscriber in the same transaction that records
the change, and a loop drains them over SMTP. A mail server that is slow or down
therefore cannot hold up polling, a delivery that fails is retried a few times
and then left with its error, and nothing is lost if the process stops between
the two steps. The feature is off unless `SMTP_ADDR` is set; while it is off the
API refuses subscriptions, rather than taking them and sending nothing. That
address is a host and a port: 465 is dialled with TLS from the first byte, which
is what the port means, and every other port is dialled plainly and upgraded with
STARTTLS when the server offers it.

## Private services

A service row can belong to one person instead of to the catalogue. Set
`owner_id` and it becomes private: still polled, but left out of the public
list, the vendor pages, the stats and the sitemap. Only its owner and the staff
can see it.

Visibility is a separate question from status. `status` says whether a row is
polled, `owner_id` says who may see it, which is why a private service is
`published`: there is nothing to moderate.

Staff move a service either way from the edit form, so a vendor that only
matters to one person can be taken out of the catalogue and put back later.

The catalogue itself is staff only. A subscriber never reaches `/services`: their
surface is the alerts they manage, and a service they want monitored goes through
the suggestion form, where a signed in visitor is recorded as the suggester.

`/services` shows the catalogue plus every private service, and is guarded by
role on both the server and the API. A private service has no page on the public
site, so its detail lives at `/services/{slug}`, which is excluded from the page
cache for the same reason as the rest of `/services`.

A private service also appears on the home page for its owner, marked `private`.
That part is fetched and merged in the browser and never rendered on the server,
which is what keeps the cached home page byte for byte identical for everybody.

## Accounts

A visitor can register on the site with a name, an address and a password. That
always makes a subscriber, and an address on the site's own domain is refused
with an answer that says no more than that the account could not be made, so the
reply cannot be used to map our own mailboxes. Registering signs the person in
and sends a link that confirms the address belongs to them; the banner on every
page offers to send it again until it does. Nothing is blocked while an address
is unconfirmed: being unable to do anything until an email arrives would be worse
than the risk it removes.

A forgotten password is reset by email too. The link works once and expires in an
hour, and using it ends every session, so if the old password was known to
somebody else they are out. Both kinds of link are stored hashed, like session
tokens, so a copy of the table is not a set of working links, and asking for
another one retires the one before it.

## Contact form

A visitor can write in from `/contact`, which is linked from the navigation and
from the narrow-screen menu. The form asks for a name, an address, a subject and
a message, all of them required, and a signed in visitor has the first two filled
in from their account. The message is mailed to `CONTACT_EMAIL`, which is the
address the site is run from, and the reply address is set to whoever wrote in,
so answering is a reply rather than a copy and paste. The subject is prefixed so
it stands out among the alert and account mail the same inbox receives.

Sending is not queued: the form waits for the mail server, because the person is
standing on the page waiting to be told it went. It is therefore refused while
`SMTP_ADDR` is unset, like alerts and account mail, and it has its own rate limit
so one visitor cannot fill the inbox.

## Staff sign in

There is no link to the login page from the site, and an administrator is still
made from the shell:

```bash
cd backend
go run ./cmd/user -email you@example.com -name "Your Name" -role admin
go run ./cmd/user -email you@example.com -reset          # new password
```

With no `-password` the command generates one and prints it once. `-role` takes
`admin`, `moderator` or `subscriber`, and an account made this way counts as
confirmed already: whoever ran the command can do anything on the server anyway.

| Role | Can do |
| --- | --- |
| `admin` | everything, including removing a published service |
| `moderator` | add, edit, publish, and delete a proposal; cannot remove a published service |
| `subscriber` | get alerts on the catalogue and suggest services; the catalogue itself is staff only |

Sessions live in an httpOnly, `SameSite=Lax` cookie, and only the hash of the
token is stored, so a database dump does not hand out working sessions. A
password reset ends every session the old password opened.

Everyone who is signed in gets their own menu, separate from the Service one:
`/account/profile` changes the display name and the address, and
`/account/password` changes the password. Changing the address takes the
confirmation with it and sends a fresh link to the new one. Changing a password
signs out every other device and keeps the one that asked, so nobody is thrown
out of the page they are standing on. Both endpoints only ask for a session, not
a role, which is what the subscriber role will need.

The management screens render on the client only, so a session never ends up in
cached HTML. `server/middleware/session-guard.ts` rejects an unauthorised request
on the server before anything is rendered, and the API checks the role again on
every call.

## Architecture notes

- **The poller reads the vendor list from the table on every cycle.** That is
  what replaced the file watcher: a row written by a form takes effect on the
  next cycle, with no reload mechanism in between.
- **History is stored as changes, not snapshots.** `status_events` only gets a
  row when a status actually changes. A per-minute snapshot would cost roughly
  1000x more storage for the same information.
- **Polling is adaptive.** 300s while healthy, 60s while degraded.
- **Empty responses never become "ok".** Unknown vocabulary maps to `unknown`
  and is recorded in `poll_runs.unknown_statuses` so drift is visible.
- **The fetch log is staff and owner only.** A vendor's public page and its row
  at `/services/{slug}` both list the recent `poll_runs` rows for it: when the
  page was read, what came back and what did not parse, detection attempts
  included. A failed fetch changes no status, so this is the only place a page
  that stopped answering shows up. The vendor itself is visible to every signed
  in visitor, so the log carries a check of its own, and on the public page it is
  rendered in the browser rather than into the cached HTML.
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
  internal/seed/          seed/vendors.yaml -> targets, with validation
  internal/poller/        reads the table each cycle, scheduling, worker pool
  internal/store/         the only package that speaks SQL
  db/migrations/          embedded .sql migrations

frontend/                 Nuxt 4 + Vue 3 + TypeScript + DaisyUI
  app/pages/              index, per-vendor, login, account, and the service screens
  app/components/         StatusBadge, the status view, the service form and its dialogs
  server/middleware/      the api proxy, and the session guard on /services and /account

compose.dev.yml           db, mailpit, api, web
```

`services.yaml` is at the root because it is the file a contributor is most
likely to touch, and it holds only the profiles: what the code can read, not
which vendors are monitored. Vendors live in the table, which is why adding one
is a row rather than a commit.

## Data sources

Everything is read from what the platform itself publishes, one adapter per
platform: Atlassian Statuspage, Status.io, Sorry, cron-job.org, FlashDuty,
PagerDuty, Slack, Better Stack and SolarWinds, with a bespoke adapter for
OpenCode's own page, whose components are slugs and whose incidents have
generated ids. The platforms that can be recognised from a url alone are also in
the detect queue, so a vendor can be added before its platform is known. Ping is
the profile to pick by hand for a service with no status page: it watches the
target by asking for it, and a request that stops coming back is the outage.
Further platforms (Instatus, RSS, HTML scraping) are additional adapters; the
adapter interface already separates "how to fetch" from "how to interpret".

Please respect each provider's `robots.txt` and terms. Attribution and a link
back to the official status page are shown on every vendor page.
