import tailwindcss from '@tailwindcss/vite'

const apiBase = process.env.NUXT_API_BASE ?? 'http://localhost:8080'
const pageCacheSeconds = Number(process.env.NUXT_PAGE_CACHE_SECONDS ?? '60')

export default defineNuxtConfig({
  compatibilityDate: '2025-07-15',
  devtools: { enabled: false },

  css: ['~/assets/css/main.css'],
  vite: {
    plugins: [tailwindcss()],
  },

  // Rendered pages are cached for 60 seconds. The underlying data is polled
  // every 5 minutes at best, so this adds no meaningful staleness, and it keeps
  // a crawler sweep from turning into thousands of server renders and database
  // queries. The API proxy runs as middleware before route rules, so /api/v1/**
  // is unaffected.
  //
  // Caching also means an edit can take up to a minute to show up in the
  // browser, so set NUXT_PAGE_CACHE_SECONDS=0 while working on the UI.
  routeRules: pageCacheSeconds > 0 ? { '/**': { swr: pageCacheSeconds } } : {},

  // The frontend calls /api/v1/... (relative) and Nitro forwards it to the Go
  // API, so SSR and the browser use the same path and no CORS is needed
  // internally. The proxy lives in server/middleware/api-proxy.ts rather than in
  // a route rule, because a route rule replaces the handler and would skip the
  // middleware that adds the internal token.
  runtimeConfig: {
    apiBase,
    internalToken: process.env.INTERNAL_TOKEN ?? '',
    public: {
      siteName: 'DownRadar',
      siteUrl: process.env.NUXT_PUBLIC_SITE_URL ?? 'http://localhost:3000',
      // Public on purpose: it is rendered as a link in the header.
      githubRepo: process.env.GITHUB_REPO ?? '',
    },
  },

  app: {
    head: {
      htmlAttrs: { lang: 'en' },
      titleTemplate: '%s | DownRadar',
      meta: [
        { charset: 'utf-8' },
        { name: 'viewport', content: 'width=device-width, initial-scale=1' },
        { name: 'theme-color', content: '#1d232a' },
      ],
      link: [{ rel: 'icon', type: 'image/svg+xml', href: '/favicon.svg' }],
    },
  },
})
