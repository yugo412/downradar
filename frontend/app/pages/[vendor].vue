<script setup lang="ts">
import type { VendorDetail } from '~/types/api'

const route = useRoute()
const slug = computed(() => String(route.params.vendor ?? ''))

const { data: vendor, error } = await useFetch<VendorDetail>(
  () => `/api/v1/vendors/${slug.value}`,
)

useHead({ titleTemplate: null })

if (vendor.value) {
  const name = vendor.value.name
  useSeoMeta({
    title: `Is ${name} down right now?`,
    description: `Live ${name} status and recent incidents, taken from the official status page. Check whether ${name} is down right now.`,
    ogTitle: `Is ${name} down right now?`,
    ogDescription: `Live status of ${name}, straight from the official status page.`,
    ogType: 'website',
  })
}

const components = computed(() => vendor.value?.componentsList ?? [])
const incidents = computed(() => vendor.value?.incidents ?? [])
const events = computed(() => vendor.value?.recentEvents ?? [])
const openIncidents = computed(() => incidents.value.filter((i) => !i.resolvedAt))
</script>

<template>
  <div class="mx-auto max-w-6xl px-4 py-8">
    <NuxtLink to="/" class="link link-hover text-sm opacity-70">&larr; All services</NuxtLink>

    <div v-if="error" role="alert" class="alert alert-error mt-6">
      <span>Service not found, or the API is still warming up.</span>
    </div>

    <template v-else-if="vendor">
      <header class="mt-4 mb-8 flex flex-wrap items-start justify-between gap-4">
        <div class="flex items-start gap-4 min-w-0">
          <VendorLogo :src="vendor.logoUrl" :name="vendor.name" :size="56" />
          <div class="min-w-0">
            <h1 class="text-3xl font-bold tracking-tight">
              Is {{ vendor.name }} down right now?
            </h1>
            <p class="mt-1 text-sm opacity-70">
              Source:
              <a :href="vendor.url" target="_blank" rel="noopener noreferrer" class="link">
                {{ vendor.url }}
              </a>
            </p>
          </div>
        </div>
        <StatusBadge :status="vendor.status" />
      </header>

      <div v-if="openIncidents.length" role="alert" class="alert alert-warning mb-8">
        <div>
          <h2 class="font-semibold">
            {{ openIncidents.length }} open incident{{ openIncidents.length > 1 ? 's' : '' }}
          </h2>
          <ul class="mt-1 text-sm list-disc list-inside">
            <li v-for="i in openIncidents" :key="i.sourceId">{{ i.name }}</li>
          </ul>
        </div>
      </div>

      <section class="mb-8">
        <h2 class="mb-3 text-lg font-semibold">Components</h2>
        <div class="overflow-x-auto bg-base-100 border border-base-300 rounded-box shadow-sm">
          <table class="table">
            <thead>
              <tr>
                <th>Component</th>
                <th>Status</th>
                <th class="text-right">Since</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="c in components" :key="c.sourceId">
                <td class="font-medium">{{ c.name }}</td>
                <td><StatusBadge :status="c.status" size="sm" /></td>
                <td class="text-right text-sm opacity-60">{{ timeAgo(c.changedAt) }}</td>
              </tr>
              <tr v-if="!components.length">
                <td colspan="3" class="text-center opacity-60">No components reported yet.</td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>

      <section v-if="incidents.length" class="mb-8">
        <h2 class="mb-3 text-lg font-semibold">Incidents</h2>
        <ul class="timeline timeline-vertical timeline-compact">
          <li v-for="i in incidents" :key="i.sourceId">
            <div class="timeline-middle">
              <span
                class="inline-block h-3 w-3 rounded-full"
                :class="i.resolvedAt ? 'bg-success' : 'bg-error'"
              />
            </div>
            <div class="timeline-end timeline-box mb-3">
              <div class="flex flex-wrap items-center gap-2">
                <span class="font-medium">{{ i.name }}</span>
                <span class="badge badge-ghost badge-sm">{{ i.status }}</span>
                <span v-if="i.impact" class="badge badge-sm">{{ i.impact }}</span>
              </div>
              <p class="mt-1 text-xs opacity-60">
                {{ formatDate(i.startedAt) }}
                <span v-if="i.resolvedAt"> → {{ formatDate(i.resolvedAt) }}</span>
                <span v-else> → ongoing</span>
              </p>
              <a
                v-if="i.url"
                :href="i.url"
                target="_blank"
                rel="noopener noreferrer"
                class="link text-xs"
              >
                Official notice
              </a>
            </div>
            <hr />
          </li>
        </ul>
      </section>

      <section v-if="events.length">
        <h2 class="mb-3 text-lg font-semibold">Recent status changes</h2>
        <ul class="timeline timeline-vertical timeline-compact">
          <li v-for="(e, idx) in events" :key="idx">
            <div class="timeline-middle">
              <span class="inline-block h-2.5 w-2.5 rounded-full bg-current opacity-60" />
            </div>
            <div class="timeline-end timeline-box mb-2">
              <div class="flex flex-wrap items-center gap-2">
                <StatusBadge :status="e.status" size="sm" />
                <span v-if="e.component" class="text-sm opacity-80">{{ e.component }}</span>
                <span class="text-xs opacity-50">{{ formatDate(e.changedAt) }}</span>
              </div>
            </div>
            <hr />
          </li>
        </ul>
      </section>

      <p v-if="vendor.lastPolledAt" class="mt-8 text-xs opacity-50">
        Last checked {{ timeAgo(vendor.lastPolledAt) }}. Data is polled every few minutes.
      </p>
    </template>

    <div v-else class="flex items-center gap-3 py-16 justify-center opacity-70">
      <span class="loading loading-spinner" />
      <span>Loading…</span>
    </div>
  </div>
</template>
