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
  releases/20261015202256/   the Go binary, services.yaml, and the web bundle
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

Edit `services.yaml` at the repository root and restart the API. No SQL, no
Go code, no database insert.

```yaml
vendors:
  - slug: strava
    name: Strava
    url: https://status.strava.com
    category: fitness
    profile: statuspage-v2
```

`services.yaml` is the source of truth. The `services` table is written by the
application on startup: new slugs are inserted, changed ones updated, and
removed ones are **soft deleted** so their incident history survives.

## Architecture notes

- **History is stored as changes, not snapshots.** `status_events` only gets a
  row when a status actually changes. A per-minute snapshot would cost roughly
  1000x more storage for the same information.
- **Polling is adaptive.** 300s while healthy, 60s while degraded.
- **Empty responses never become "ok".** Unknown vocabulary maps to `unknown`
  and is recorded in `poll_runs.unknown_statuses` so drift is visible.
- **`/api/**` is proxied by Nuxt**, so the browser and SSR use the same relative
  path and no CORS is involved.

## Layout

```
services.yaml             the vendor registry (source of truth)

backend/                  Go: poller, adapters, store, API
  cmd/server/             entrypoint
  internal/adapters/      one adapter per platform, not per vendor
  internal/catalog/       services.yaml -> targets, with validation
  internal/poller/        scheduling and worker pool
  internal/store/         the only package that speaks SQL
  db/migrations/          embedded .sql migrations

frontend/                 Nuxt 4 + Vue 3 + TypeScript + DaisyUI
  app/pages/              index and per-vendor pages
  app/components/         StatusBadge and friends

compose.dev.yml           db, mailpit, api, web
```

The registry sits at the root because it is the one file most likely to be
edited. The API watches it and reloads in place: saving it takes effect within
a couple of seconds, with no restart and no dropped requests. If the new
content fails validation the previous configuration keeps serving, and the
error is logged with the offending vendor.

## Data sources

Everything currently configured uses Atlassian Statuspage and its public
`/api/v2/summary.json` endpoint. Support for other platforms (Instatus, RSS,
HTML scraping) is planned as additional adapters; the adapter interface already
separates "how to fetch" from "how to interpret".

Please respect each provider's `robots.txt` and terms. Attribution and a link
back to the official status page are shown on every vendor page.
