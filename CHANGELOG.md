# Changelog

All notable changes to DownRadar are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and releases use
semantic versioning.

## [0.7.0]

### Added

- Lark alerts, alongside email, webhook, Telegram, Slack and Discord.
- Generated vendor summaries written from the official status data, refreshed
  only when the vendor state changes and shown above the open incidents.
- RSS and JSON Feed for status changes.
- Sign in by email link, GitHub and Google, plus linked provider management.
- Subscriber-managed services, including suggestions that become published
  after profile detection.
- Account deletion and its confirmation email.
- A read-only MCP server for the public catalogue.
- Generated Open Graph images for vendor pages.
- Privacy and terms pages.

### Changed

- Webhooks can use Standard Webhooks signatures or custom headers, and channel
  settings are sealed at rest.
- Alerts wait for a status change to settle; channels can be paused from their
  rows and the webhook panel is split into tabs.
- The status filter uses the daisyUI filter component, and the components table
  can be sorted by name, state or since.
- Open incidents are collapsed by default on vendor pages.
- The first visit follows the system colour scheme, and theme changes use one
  view transition.
- Feed URLs and their copy button are joined, and staff can correct a service
  address from the edit screen.
- The README is more compact, and one-word confirmations no longer carry full
  stops.

### Fixed

- Read the status pages that the detect queue could not match.
- Leave a refusing status page alone until its retry window ends.
- Keep personal contact defaults out of the repository.
- Show the vendor logo and API page layout correctly.
- Update the Go modules and CI actions.

## [0.6.1] - 2026-10-19

### Fixed

- Pass image names through sudo in the deploy.

## [0.6.0] - 2026-10-19

### Added

- Docker-based deployment from CI.

## [0.5.0] - 2026-10-19

### Added

- Webhook alerts alongside email.
- Filters, sorting and paging for the alerts list.
- A Subscribed filter on the index.
- The names of down services in the index heading, coloured by status.
- The status filter and sort kept in the index URL.

### Changed

- The Unknown state has a background of its own.

## [0.4.0] - 2026-10-17

### Added

- Component-level alert subscriptions.
- HTML email templates for every message.
- An About page.
- A SolarWinds adapter and a directory for each adapter.
- Sorting, paging and an alerts column in the admin user list.
- A status filter on service lists and an updated-at sort.
- Paused alerts marked on the index.

### Changed

- Alerts are split into subscriptions and channels.
- Messages start with a capital letter.

## [0.3.0] - 2026-10-16

### Added

- Email alerts for vendor status changes.
- Self-service account creation with email confirmation.
- A Better Stack adapter, with vendor-specific profiles hidden.
- A contact form.
- Per-user alert limits.

## [0.2.0] - 2026-10-16

### Added

- OpenCode, Status.io, Sorry and ping adapters.
- An admin user list.
- A profile detection queue and a fetch log for staff.
- Custom 404 and 500 pages.
- Live relative times on the index.

### Changed

- The navigation collapses into a hamburger on narrow screens.
- The theme choice is kept out of cached pages.
- The Makefile targets are declared phony.

## [0.1.1] - 2026-10-16

### Fixed

- Release artifacts include the seed and user commands.

## [0.1.0] - 2026-10-16

### Added

- Initial public release: status aggregation from Statuspage, Slack, CronJob
  and FlashDuty.
- A database-backed vendor catalogue, accounts, private services and the public
  API.
- Deployment with systemd, Caddy and release symlinks, with CI building on tags.

### Fixed

- Guard page identity, keep raw payloads and cache rendered pages.
- Preserve search input keystrokes and the schedules.run logo.
