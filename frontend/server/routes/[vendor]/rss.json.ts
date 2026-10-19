import { jsonDocument, loadVendorFeed } from '../../utils/feed'

// The same feed in JSON Feed, for a reader that would rather have JSON than XML.
export default defineEventHandler(async (event) => {
  const feed = await loadVendorFeed(getRouterParam(event, 'vendor') ?? '', 'json')
  if (!feed) {
    throw createError({ statusCode: 404, statusMessage: 'Vendor not found' })
  }

  setHeader(event, 'Content-Type', 'application/feed+json; charset=utf-8')
  setHeader(event, 'Cache-Control', 'public, max-age=60, stale-while-revalidate=300')
  return jsonDocument(feed)
})
