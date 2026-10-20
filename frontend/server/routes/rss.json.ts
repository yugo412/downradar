import { jsonDocument, loadSiteFeed } from '../utils/feed'

// The same site-wide feed in JSON Feed.
export default defineEventHandler(async (event) => {
  const feed = await loadSiteFeed('json')

  setHeader(event, 'Content-Type', 'application/feed+json; charset=utf-8')
  setHeader(event, 'Cache-Control', 'public, max-age=60, stale-while-revalidate=300')
  return jsonDocument(feed)
})
