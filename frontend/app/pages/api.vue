<script setup lang="ts">
const { siteUrl } = useRuntimeConfig().public
const base = siteUrl.replace(/\/$/, '')

const fields = [
  { name: 'slug', type: 'string', desc: 'Stable identifier. Use it to build the vendor page URL.' },
  { name: 'name', type: 'string', desc: 'Display name of the service.' },
  { name: 'url', type: 'string', desc: "The service's official status page, the source of the data." },
  { name: 'category', type: 'string', desc: 'Optional grouping label, curated by hand.' },
  { name: 'logoUrl', type: 'string', desc: 'Optional logo. May be absent.' },
  { name: 'status', type: 'string', desc: 'One of the status values listed below.' },
  { name: 'changedAt', type: 'string | null', desc: 'When the status last changed. Not the last check time.' },
  { name: 'components', type: 'number', desc: 'How many components the status page reports.' },
  { name: 'degradedComponents', type: 'number', desc: 'How many of those are not operational.' },
  { name: 'lastPolledAt', type: 'string | null', desc: 'When this service was last checked.' },
]

const statuses = [
  { value: 'ok', meaning: 'Fully operational' },
  { value: 'degraded', meaning: 'Slower or partially affected, still usable' },
  { value: 'partial', meaning: 'Some components are having an outage' },
  { value: 'major', meaning: 'Critical components are down' },
  { value: 'maintenance', meaning: 'Planned maintenance' },
  { value: 'unknown', meaning: 'No data, or vocabulary we do not recognise' },
]

const example = `{
  "count": 2,
  "vendors": [
    {
      "slug": "elastic",
      "name": "Elastic",
      "url": "https://status.elastic.co",
      "category": "observability",
      "logoUrl": "https://dka575ofm4ao0.cloudfront.net/pages-favicon_logos/original/862/favicon.ico",
      "status": "partial",
      "changedAt": "2026-10-15T10:45:06Z",
      "components": 548,
      "degradedComponents": 7,
      "lastPolledAt": "2026-10-15T11:31:04Z"
    },
    {
      "slug": "strava",
      "name": "Strava",
      "url": "https://status.strava.com",
      "category": "fitness",
      "logoUrl": "https://dka575ofm4ao0.cloudfront.net/pages-favicon_logos/original/8442/qdVJwxWnSKqnbylGekz0",
      "status": "ok",
      "changedAt": "2026-10-15T10:45:04Z",
      "components": 6,
      "degradedComponents": 0,
      "lastPolledAt": "2026-10-15T11:31:03Z"
    }
  ]
}`

useSeoMeta({
  title: 'Public API',
  description:
    'A free, read-only JSON endpoint with the current status of every monitored service.',
})
</script>

<template>
  <div class="mx-auto max-w-6xl px-4 py-8">
    <div class="rounded-box border border-base-300 bg-base-100 p-5 md:p-8">
      <h1 class="text-3xl font-bold tracking-tight">Public API</h1>
      <p class="mt-2 opacity-70">
        Free, read-only JSON endpoints with the current status of every monitored
        service. No key, no signup.
      </p>

      <section class="mt-8">
        <h2 class="text-lg font-semibold">Endpoints</h2>
        <div class="mt-3 mockup-code bg-base-300 text-base-content">
          <pre data-prefix="GET"><code>{{ base }}/api/v1/vendors</code></pre>
          <pre data-prefix="GET"><code>{{ base }}/api/v1/vendors/{slug}</code></pre>
          <pre data-prefix="GET"><code>{{ base }}/api/v1/changes</code></pre>
        </div>
        <p class="mt-3 text-sm opacity-70">
          The first returns every monitored service, around 25 KB for 71 services.
          The second returns one service with its components, recent status changes
          and incidents. The third returns the newest status changes across every
          service, newest first, with <code class="font-mono">?limit=</code> to ask
          for fewer than the fifty it gives, up to a hundred.
        </p>
      </section>

      <section class="mt-8">
        <h2 class="text-lg font-semibold">Feeds</h2>
        <div class="mt-3 mockup-code bg-base-300 text-base-content">
          <pre data-prefix="GET"><code>{{ base }}/rss.xml</code></pre>
          <pre data-prefix="GET"><code>{{ base }}/rss.json</code></pre>
          <pre data-prefix="GET"><code>{{ base }}/{slug}/rss.xml</code></pre>
          <pre data-prefix="GET"><code>{{ base }}/{slug}/rss.json</code></pre>
        </div>
        <p class="mt-3 text-sm opacity-70">
          Every status change as a feed, in RSS or in JSON Feed: one for the whole
          site and one for each service, each carrying its newest changes. They need
          no account, nothing is subscribed to and nothing is stored, so a reader
          that follows one costs no more than a visit to the page it is about.
        </p>
      </section>

      <section class="mt-8">
        <h2 class="text-lg font-semibold">Example</h2>
        <div class="mt-3 mockup-code bg-base-300 text-base-content">
          <pre data-prefix="$"><code>curl -s {{ base }}/api/v1/vendors</code></pre>
        </div>
        <pre class="mt-3 overflow-x-auto rounded-box border border-base-300 bg-base-100 p-4 text-xs leading-relaxed">{{ example }}</pre>
      </section>

      <section class="mt-8">
        <h2 class="text-lg font-semibold">Fields</h2>
        <div class="mt-3 overflow-x-auto rounded-box border border-base-300 bg-base-100">
          <table class="table">
            <thead>
              <tr>
                <th>Field</th>
                <th>Type</th>
                <th>Description</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="f in fields" :key="f.name">
                <td class="font-mono text-sm">{{ f.name }}</td>
                <td class="text-sm opacity-70">{{ f.type }}</td>
                <td class="text-sm">{{ f.desc }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>

      <section class="mt-8">
        <h2 class="text-lg font-semibold">Status values</h2>
        <div class="mt-3 overflow-x-auto rounded-box border border-base-300 bg-base-100">
          <table class="table">
            <thead>
              <tr>
                <th>Value</th>
                <th>Meaning</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="s in statuses" :key="s.value">
                <td><StatusBadge :status="s.value" size="sm" /></td>
                <td class="text-sm">{{ s.meaning }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>

      <section class="mt-8">
        <h2 class="text-lg font-semibold">Rate limits</h2>
        <ul class="mt-3 list-disc list-inside space-y-1 text-sm opacity-80">
          <li>
            <strong>120 requests per minute per IP</strong>, with a burst of 30.
            This is far more than a normal integration needs, and it exists to
            stop abuse rather than to meter usage.
          </li>
          <li>
            Exceeding it returns
            <code class="font-mono">429 Too Many Requests</code> with a
            <code class="font-mono">Retry-After: 60</code> header and a small JSON
            body.
          </li>
          <li>
            Because responses are cacheable for a minute, caching on your side is
            the easiest way to stay well inside the limit.
          </li>
        </ul>
      </section>

      <section class="mt-8">
        <h2 class="text-lg font-semibold">Caching and freshness</h2>
        <ul class="mt-3 list-disc list-inside space-y-1 text-sm opacity-80">
          <li>
            Responses carry
            <code class="font-mono">Cache-Control: public, max-age=60, stale-while-revalidate=300</code>,
            so caching for a minute is safe.
          </li>
          <li>
            Each service is polled every 5 minutes while healthy, and every minute
            while it is having problems. Data can therefore be up to 5 minutes old.
          </li>
          <li>
            <code class="font-mono">changedAt</code> is when the status last
            changed, which can be much older than
            <code class="font-mono">lastPolledAt</code>.
          </li>
        </ul>
      </section>

      <section class="mt-8">
        <h2 class="text-lg font-semibold">Suggesting a service</h2>
        <p class="mt-3 text-sm opacity-80">
          If a service is missing, the website offers to suggest it. That form posts
          to <code class="font-mono">POST /api/v1/suggest</code> with
          <code class="font-mono">{ "name": "...", "url": "..." }</code>, where the
          url is optional. The suggestion is stored for review, not monitored right
          away, and a moderator publishes it later. It is rate limited much more
          tightly than the read endpoints.
        </p>
      </section>

      <section class="mt-8">
        <h2 class="text-lg font-semibold">Terms and privacy</h2>
        <p class="mt-3 text-sm opacity-80">
          Free to use, including commercially. Please cache rather than polling
          aggressively, and link back to
          <a :href="base" class="link">{{ base }}</a> where it makes sense. The data
          comes from each service's own status page, so treat it as a convenience
          rather than a contract. Read the
          <NuxtLink to="/terms" class="link">Terms of Service</NuxtLink> and
          <NuxtLink to="/privacy" class="link">Privacy Policy</NuxtLink>.
        </p>
      </section>
    </div>
  </div>
</template>
