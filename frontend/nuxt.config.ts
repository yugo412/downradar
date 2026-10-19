import tailwindcss from '@tailwindcss/vite'

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
  // Only the page cache is decided at build time, because route rules are baked
  // into the bundle. Everything else is supplied at runtime by environment
  // variables, using Nuxt's naming convention: runtimeConfig.apiBase comes from
  // NUXT_API_BASE, runtimeConfig.internalToken from NUXT_INTERNAL_TOKEN, and
  // runtimeConfig.public.x from NUXT_PUBLIC_X.
  //
  // Never read a secret from process.env in this file. Nuxt inlines those values
  // into the bundle at build time, so a build done on a laptop would ship the
  // local secret to production and every internal check would then fail.
  routeRules:
    pageCacheSeconds > 0
      ? {
          '/**': { swr: pageCacheSeconds },
          // The management and account screens are per visitor, so a cached
          // shell would be handed to the wrong one. They also render on the
          // client only, which keeps any session out of the cached HTML.
          '/services': { swr: false, headers: { 'cache-control': 'no-store' } },
          '/services/**': { swr: false, headers: { 'cache-control': 'no-store' } },
          '/account': { swr: false, headers: { 'cache-control': 'no-store' } },
          '/account/**': { swr: false, headers: { 'cache-control': 'no-store' } },
          '/users': { swr: false, headers: { 'cache-control': 'no-store' } },
          '/login': { swr: false, headers: { 'cache-control': 'no-store' } },
        }
      : {},

  runtimeConfig: {
    apiBase: 'http://127.0.0.1:8080',
    internalToken: '',
    public: {
      siteName: 'DownRadar',
      siteUrl: 'http://localhost:3000',
      githubRepo: '',
    },
  },

  app: {
    head: {
      htmlAttrs: { lang: 'en' },
      script: [
        {
          // Runs in the head, before the first paint, so there is no flash. It
          // reads the cookie itself instead of relying on the rendered head,
          // which is cached and has to stay the same for every visitor.
          //
          // A visitor with no cookie has not chosen a theme, so the system
          // gets to: the dark one when the system asks for it, the light one
          // otherwise. The cookie still wins, so a theme that was picked on
          // purpose is kept.
          innerHTML: `(function(){try{var m=document.cookie.match(/(?:^|;\\s*)theme=(dark|cmyk)/);var t=m?m[1]:(window.matchMedia&&window.matchMedia('(prefers-color-scheme: dark)').matches?'dark':'cmyk');document.documentElement.dataset.theme=t}catch(e){document.documentElement.dataset.theme='cmyk'}})()`,
          tagPosition: 'head',
        },
      ],
      titleTemplate: '%s - DownRadar',
      meta: [
        { charset: 'utf-8' },
        { name: 'viewport', content: 'width=device-width, initial-scale=1' },
        { name: 'theme-color', content: '#1d232a' },
      ],
      link: [{ rel: 'icon', type: 'image/svg+xml', href: '/favicon.svg' }],
    },
  },
})
