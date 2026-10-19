// The feeds are the site's history in the two shapes a reader can ask for: RSS,
// which is XML, and JSON Feed, which is JSON. Both carry the same items, so they
// are built from one description of a change rather than from two.
//
// Nothing here is stored and nothing is subscribed to: a feed is a view of the
// status events that are already kept for the pages, which is why it costs a
// reader no more than a visit to the page it is about.
import { formatDate, statusMeta } from '../../app/utils/format'

// What a vendor feed takes its changes from: the vendor's own page, which already
// carries the vendor's name, address and status beside its recent events.
export interface ApiVendor {
  slug: string
  name: string
  url?: string
  status: string
  recentEvents?: {
    id: number
    status: string
    previousStatus?: string
    component?: string
    changedAt: string
  }[]
}

// What the changes endpoint returns, which is what a feed for the whole site is
// built from.
export interface ApiChange {
  id: number
  vendorSlug: string
  vendorName: string
  component?: string
  from?: string
  to: string
  changedAt: string
}

// One change as the feed tells it. The id is the event's own, so a reader that
// keeps what it has seen does not show the same change twice.
export interface FeedChange {
  id: string
  vendorName: string
  vendorSlug: string
  component: string
  from: string
  to: string
  changedAt: string
}

export interface FeedItem {
  id: string
  title: string
  url: string
  text: string
  at: string
}

export interface FeedMeta {
  title: string
  description: string
  homeUrl: string
  feedUrl: string
}

export interface Feed {
  meta: FeedMeta
  items: FeedItem[]
}

// The words a change is told in. A change with no starting point, which is one
// recorded before the column existed, says only what it is now rather than
// claiming a move nobody saw.
function sentence(change: FeedChange, withVendor: boolean): string {
  const what = change.component || 'the whole service'
  const subject = withVendor
    ? `${change.vendorName}: ${what}`
    : `${what.charAt(0).toUpperCase()}${what.slice(1)}`

  if (!change.from) return `${subject} is now ${statusMeta(change.to).label}`
  return `${subject} moved from ${statusMeta(change.from).label} to ${statusMeta(change.to).label}`
}

function items(changes: FeedChange[], siteUrl: string, withVendor: boolean): FeedItem[] {
  return changes.map((change) => {
    const told = sentence(change, withVendor)
    return {
      id: `downradar:event:${change.id}`,
      title: told,
      url: `${siteUrl}/${change.vendorSlug}`,
      text: `${told} on ${formatDate(change.changedAt)}.`,
      at: change.changedAt,
    }
  })
}

function statusOf(error: unknown): number {
  const err = error as { statusCode?: number; status?: number }
  return err?.statusCode ?? err?.status ?? 0
}

// What the site and the API are, read here so a route only has to say which feed
// it is serving. The internal token is what the proxy sends for the browser, and
// it says the same thing here: this is the site asking, not a third party, so a
// feed being polled does not eat into the rate limit the public shares.
function settings() {
  const config = useRuntimeConfig()
  return {
    apiBase: String(config.apiBase),
    siteUrl: String(config.public.siteUrl).replace(/\/$/, ''),
    siteName: String(config.public.siteName),
    headers: {
      'x-internal-token': String(config.internalToken),
      accept: 'application/json',
    },
  }
}

// A vendor feed names the vendor once, in its title, so an item does not have to
// repeat it. A feed for the whole site has no such title, so every item says
// whose change it was.
export async function loadVendorFeed(slug: string, format: 'xml' | 'json'): Promise<Feed | null> {
  const site = settings()

  let vendor: ApiVendor
  try {
    vendor = await $fetch<ApiVendor>(
      `${site.apiBase}/api/v1/vendors/${encodeURIComponent(slug)}`,
      { headers: site.headers },
    )
  } catch (error) {
    // A slug nobody has is a feed that does not exist. Anything else is the API
    // being unreachable, which is not the reader's mistake and is not dressed up
    // as a missing feed.
    if (statusOf(error) === 404) return null
    throw error
  }

  const changes: FeedChange[] = (vendor.recentEvents ?? [])
    // A feed carries changes, and an event with nothing before it is the state a
    // target was first seen in rather than a move anybody was told about.
    .filter((event) => Boolean(event.previousStatus))
    .map((event) => ({
      id: String(event.id),
      vendorName: vendor.name,
      vendorSlug: vendor.slug,
      component: event.component ?? '',
      from: event.previousStatus ?? '',
      to: event.status,
      changedAt: event.changedAt,
    }))

  return {
    meta: {
      title: `${site.siteName}: ${vendor.name} status changes`,
      description: `Status changes for ${vendor.name}, taken from its official status page.`,
      homeUrl: `${site.siteUrl}/${vendor.slug}`,
      feedUrl: `${site.siteUrl}/${vendor.slug}/rss.${format}`,
    },
    items: items(changes, site.siteUrl, false),
  }
}

export async function loadSiteFeed(format: 'xml' | 'json'): Promise<Feed> {
  const site = settings()

  let changes: ApiChange[] = []
  try {
    const res = await $fetch<{ changes: ApiChange[] }>(
      `${site.apiBase}/api/v1/changes?limit=50`,
      { headers: site.headers },
    )
    changes = res.changes ?? []
  } catch {
    changes = []
  }

  const mapped: FeedChange[] = changes.map((change) => ({
    id: String(change.id),
    vendorName: change.vendorName,
    vendorSlug: change.vendorSlug,
    component: change.component ?? '',
    from: change.from ?? '',
    to: change.to,
    changedAt: change.changedAt,
  }))

  return {
    meta: {
      title: `${site.siteName} status changes`,
      description: `Status changes across the services ${site.siteName} follows, taken from their official status pages.`,
      homeUrl: `${site.siteUrl}/`,
      feedUrl: `${site.siteUrl}/rss.${format}`,
    },
    items: items(mapped, site.siteUrl, true),
  }
}

function escapeXml(value: string): string {
  return value
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&apos;')
}

// The build time is the newest change rather than the moment of the request, so a
// cached copy and a fresh one say the same thing. A feed with nothing in it has no
// time to give and leaves the element out rather than claiming it changed when it
// was asked for.
function lastBuild(items: FeedItem[]): string {
  const newest = items.reduce<string>((latest, item) => (item.at > latest ? item.at : latest), '')
  return newest ? new Date(newest).toUTCString() : ''
}

export function rssDocument(feed: Feed): string {
  const lines = [
    '<?xml version="1.0" encoding="UTF-8"?>',
    '<rss version="2.0" xmlns:atom="http://www.w3.org/2005/Atom">',
    '  <channel>',
    `    <title>${escapeXml(feed.meta.title)}</title>`,
    `    <link>${escapeXml(feed.meta.homeUrl)}</link>`,
    `    <description>${escapeXml(feed.meta.description)}</description>`,
    '    <language>en</language>',
  ]

  const built = lastBuild(feed.items)
  if (built) lines.push(`    <lastBuildDate>${built}</lastBuildDate>`)

  lines.push(
    `    <atom:link href="${escapeXml(feed.meta.feedUrl)}" rel="self" type="application/rss+xml" />`,
  )

  for (const item of feed.items) {
    lines.push(
      '    <item>',
      `      <title>${escapeXml(item.title)}</title>`,
      `      <link>${escapeXml(item.url)}</link>`,
      `      <guid isPermaLink="false">${escapeXml(item.id)}</guid>`,
      `      <pubDate>${new Date(item.at).toUTCString()}</pubDate>`,
      `      <description>${escapeXml(item.text)}</description>`,
      '    </item>',
    )
  }

  lines.push('  </channel>', '</rss>', '')
  return lines.join('\n')
}

export function jsonDocument(feed: Feed): Record<string, unknown> {
  return {
    version: 'https://jsonfeed.org/version/1.1',
    title: feed.meta.title,
    home_page_url: feed.meta.homeUrl,
    feed_url: feed.meta.feedUrl,
    description: feed.meta.description,
    language: 'en',
    items: feed.items.map((item) => ({
      id: item.id,
      url: item.url,
      title: item.title,
      content_text: item.text,
      date_published: item.at,
    })),
  }
}
