interface SitemapVendor {
  slug: string
  lastPolledAt?: string | null
}

function escapeXml(value: string): string {
  return value
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&apos;')
}

export default defineEventHandler(async (event) => {
  const config = useRuntimeConfig()
  const siteUrl = config.public.siteUrl.replace(/\/$/, '')

  let vendors: SitemapVendor[] = []
  try {
    const data = await $fetch<{ vendors: SitemapVendor[] }>(`${config.apiBase}/api/v1/vendors`)
    vendors = data.vendors ?? []
  } catch {
    vendors = []
  }

  const entries: string[] = [
    `  <url><loc>${escapeXml(siteUrl)}/</loc><changefreq>hourly</changefreq><priority>1.0</priority></url>`,
    `  <url><loc>${escapeXml(siteUrl)}/about</loc><changefreq>monthly</changefreq><priority>0.5</priority></url>`,
  ]

  for (const vendor of vendors) {
    const lastmod = vendor.lastPolledAt
      ? `<lastmod>${new Date(vendor.lastPolledAt).toISOString()}</lastmod>`
      : ''
    entries.push(
      `  <url><loc>${escapeXml(`${siteUrl}/${vendor.slug}`)}</loc>${lastmod}<changefreq>hourly</changefreq><priority>0.8</priority></url>`,
    )
  }

  const xml = [
    '<?xml version="1.0" encoding="UTF-8"?>',
    '<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">',
    ...entries,
    '</urlset>',
    '',
  ].join('\n')

  setHeader(event, 'Content-Type', 'application/xml; charset=utf-8')
  setHeader(event, 'Cache-Control', 'public, max-age=600')
  return xml
})
