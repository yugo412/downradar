import { loadSiteFeed, rssDocument } from '../utils/feed'

// The feed for the whole site, in RSS: the newest changes across every service
// the site follows.
export default defineEventHandler(async (event) => {
  const feed = await loadSiteFeed('xml')

  setHeader(event, 'Content-Type', 'application/rss+xml; charset=utf-8')
  setHeader(event, 'Cache-Control', 'public, max-age=60, stale-while-revalidate=300')
  return rssDocument(feed)
})
