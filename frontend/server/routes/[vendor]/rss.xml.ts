import { loadVendorFeed, rssDocument } from '../../utils/feed'

// The feed for one vendor, in RSS. It is a view of that vendor's status events
// rather than something anybody subscribes to, so it needs no account and holds
// nothing of its own.
export default defineEventHandler(async (event) => {
  const feed = await loadVendorFeed(getRouterParam(event, 'vendor') ?? '', 'xml')
  if (!feed) {
    throw createError({ statusCode: 404, statusMessage: 'Vendor not found' })
  }

  setHeader(event, 'Content-Type', 'application/rss+xml; charset=utf-8')
  setHeader(event, 'Cache-Control', 'public, max-age=60, stale-while-revalidate=300')
  return rssDocument(feed)
})
