<script setup lang="ts">
import type { Stats, Vendor } from '~/types/api'

const { data, error } = await useFetch<{ vendors: Vendor[]; count: number }>('/api/v1/vendors')
const { data: stats } = await useFetch<Stats>('/api/v1/stats')

const route = useRoute()

// The inputs are bound to a local ref, not to the route query. navigateTo is
// asynchronous, so reading the value back from the URL while typing re-renders
// the field with a stale value and swallows keystrokes. The URL is updated
// separately and debounced, so a search is still shareable.
const queryInput = ref(String(route.query.q ?? ''))
const query = computed(() => queryInput.value)

let urlSyncTimer: ReturnType<typeof setTimeout> | undefined
watch(queryInput, (value) => {
  clearTimeout(urlSyncTimer)
  urlSyncTimer = setTimeout(() => {
    const trimmed = value.trim()
    if (trimmed === String(route.query.q ?? '')) return
    navigateTo({ query: trimmed ? { q: trimmed } : {} }, { replace: true })
  }, 250)
})

// Follow the URL when it changes from somewhere else, such as the back button
// or a shared link, without fighting the typing above.
watch(
  () => route.query.q,
  (value) => {
    const next = String(value ?? '')
    if (next !== queryInput.value.trim()) queryInput.value = next
  },
)

const worstFirst = ref(true)

const vendors = computed<Vendor[]>(() => data.value?.vendors ?? [])

const visible = computed(() => {
  const q = query.value.trim().toLowerCase()
  const base = q
    ? vendors.value.filter(
        (v) =>
          v.name.toLowerCase().includes(q) ||
          v.slug.includes(q) ||
          (v.category ?? '').toLowerCase().includes(q),
      )
    : vendors.value

  return [...base].sort((a, b) =>
    worstFirst.value
      ? severity(b.status) - severity(a.status) || a.name.localeCompare(b.name)
      : a.name.localeCompare(b.name),
  )
})

type SuggestState = 'idle' | 'sending' | 'sent' | 'manual' | 'exists' | 'error'

const suggestState = ref<SuggestState>('idle')
const suggestUrl = ref('')
const suggestMessage = ref('')
const suggestLink = ref('')

const suggestName = computed({
  get: () => queryInput.value,
  set: (value: string) => {
    queryInput.value = value
  },
})

watch(query, () => {
  if (suggestState.value === 'sending') return
  suggestState.value = 'idle'
  suggestMessage.value = ''
  suggestLink.value = ''
})

async function submitSuggestion() {
  suggestState.value = 'sending'
  suggestMessage.value = ''
  suggestLink.value = ''

  try {
    const result = await $fetch<{ issueUrl?: string; manualUrl?: string }>('/api/v1/suggest', {
      method: 'POST',
      body: { name: suggestName.value, url: suggestUrl.value },
    })

    if (result.issueUrl) {
      suggestState.value = 'sent'
      suggestLink.value = result.issueUrl
    } else if (result.manualUrl) {
      suggestState.value = 'manual'
      suggestLink.value = result.manualUrl
    } else {
      suggestState.value = 'sent'
    }
  } catch (err: unknown) {
    const data = (err as { data?: { error?: string; vendorSlug?: string; manualUrl?: string } })?.data

    if (data?.vendorSlug) {
      suggestState.value = 'exists'
      suggestMessage.value = data.error ?? 'That service is already monitored.'
      suggestLink.value = `/${data.vendorSlug}`
    } else if (data?.manualUrl) {
      suggestState.value = 'manual'
      suggestLink.value = data.manualUrl
    } else {
      suggestState.value = 'error'
      suggestMessage.value = data?.error ?? 'Could not send the suggestion. Please try again.'
    }
  }
}

useSeoMeta({
  title: 'Real-time status of popular services',
  description:
    'Aggregated status of popular services, taken directly from their official status pages. See what is down right now.',
  ogTitle: 'DownRadar — is it down right now?',
  ogDescription: 'Live status of popular services, straight from their official status pages.',
  ogType: 'website',
})
</script>

<template>
  <div class="mx-auto max-w-6xl px-4 py-8">
    <section class="mb-8">
      <h1 class="text-3xl font-bold tracking-tight">Is it down right now?</h1>
      <p class="mt-2 opacity-70">
        Status taken directly from each service's official status page. No crowdsourced guessing.
      </p>
    </section>

    <section
      v-if="stats"
      class="stats stats-vertical sm:stats-horizontal w-full mb-8 bg-base-100 border border-base-300 rounded-box shadow-sm"
    >
      <div class="stat">
        <div class="stat-title">Monitored</div>
        <div class="stat-value text-2xl">{{ stats.total }}</div>
      </div>
      <div class="stat">
        <div class="stat-title">Operational</div>
        <div class="stat-value text-2xl text-success">{{ stats.healthy }}</div>
      </div>
      <div class="stat">
        <div class="stat-title">Degraded</div>
        <div class="stat-value text-2xl text-warning">{{ stats.degraded }}</div>
      </div>
      <div class="stat">
        <div class="stat-title">Major outage</div>
        <div class="stat-value text-2xl text-error">{{ stats.down }}</div>
      </div>
    </section>

    <section class="mb-6 flex flex-col gap-3 sm:flex-row sm:items-center">
      <input
        v-model="queryInput"
        type="search"
        placeholder="Search a service, e.g. strava"
        class="input input-bordered w-full sm:max-w-sm"
      />
      <label class="label cursor-pointer gap-2">
        <input v-model="worstFirst" type="checkbox" class="toggle toggle-sm" />
        <span class="label-text text-sm">Show problems first</span>
      </label>
    </section>

    <div v-if="error" role="alert" class="alert alert-error">
      <span>Could not load the status list. The API may still be warming up.</span>
    </div>

    <div v-else-if="!vendors.length" class="flex items-center gap-3 py-16 justify-center opacity-70">
      <span class="loading loading-spinner" />
      <span>Waiting for the first poll cycle…</span>
    </div>

    <template v-else>
      <div v-if="!visible.length" class="card bg-base-100 border border-base-300">
        <div class="card-body gap-3">
          <div>
            <h2 class="text-lg font-semibold">No match for “{{ query }}”</h2>
            <p class="mt-1 text-sm opacity-70">
              We do not monitor that service yet. Suggest it and we will take a look.
            </p>
          </div>

          <div v-if="suggestState === 'sent'" role="status" class="alert alert-success">
            <div>
              <p class="font-medium">Thanks, your suggestion was sent.</p>
              <a
                v-if="suggestLink"
                :href="suggestLink"
                target="_blank"
                rel="noopener noreferrer"
                class="link text-sm"
              >
                Track it on GitHub
              </a>
            </div>
          </div>

          <div v-else-if="suggestState === 'manual'" role="status" class="alert alert-info">
            <div>
              <p class="font-medium">
                Automatic submission is not configured. Open the issue to finish.
              </p>
              <a
                :href="suggestLink"
                target="_blank"
                rel="noopener noreferrer"
                class="btn btn-sm btn-primary mt-2"
              >
                Open the GitHub issue
              </a>
            </div>
          </div>

          <div v-else-if="suggestState === 'exists'" role="status" class="alert alert-warning">
            <div>
              <p class="font-medium">{{ suggestMessage }}</p>
              <NuxtLink :to="suggestLink" class="link text-sm">Open its status page</NuxtLink>
            </div>
          </div>

          <form v-else class="flex flex-col gap-3" @submit.prevent="submitSuggestion">
            <div class="grid gap-3 sm:grid-cols-2">
              <label class="form-control w-full">
                <span class="label-text mb-1 text-sm">Service name</span>
                <input
                  v-model="suggestName"
                  type="text"
                  class="input input-bordered w-full"
                  placeholder="e.g. Some Service"
                  maxlength="80"
                  required
                />
              </label>
              <label class="form-control w-full">
                <span class="label-text mb-1 text-sm">Status page URL (optional)</span>
                <input
                  v-model="suggestUrl"
                  type="url"
                  class="input input-bordered w-full"
                  placeholder="https://status.example.com"
                  maxlength="300"
                />
              </label>
            </div>

            <div class="flex flex-wrap items-center gap-3">
              <button
                type="submit"
                class="btn btn-primary btn-sm"
                :disabled="suggestState === 'sending'"
              >
                <span v-if="suggestState === 'sending'" class="loading loading-spinner loading-xs" />
                {{ suggestState === 'sending' ? 'Sending…' : 'Suggest this service' }}
              </button>
              <span class="text-xs opacity-60">
                Opens a GitHub issue so the request can be tracked.
              </span>
            </div>

            <p v-if="suggestState === 'error'" class="text-sm text-error">{{ suggestMessage }}</p>
          </form>
        </div>
      </div>

      <section v-else class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        <NuxtLink
          v-for="v in visible"
          :key="v.slug"
          :to="`/${v.slug}`"
          class="card bg-base-100 border border-base-300 hover:border-primary transition-colors"
        >
          <div class="card-body p-4">
            <div class="flex items-start gap-3">
              <VendorLogo :src="v.logoUrl" :name="v.name" :size="40" />
              <div class="min-w-0 flex-1">
                <div class="flex items-center justify-between gap-2">
                  <h2 class="text-lg font-semibold leading-snug">{{ v.name }}</h2>
                  <StatusBadge :status="v.status" size="sm" class="shrink-0" />
                </div>
                <p class="text-xs opacity-60">{{ v.category || 'service' }}</p>
              </div>
            </div>

            <div class="mt-3 flex flex-wrap items-center justify-between gap-x-2 gap-y-1 text-xs opacity-60">
              <span>{{ v.components }} components</span>
              <span>
                {{ timeAgo(v.changedAt) }} in this state &middot; checked {{ timeAgo(v.lastPolledAt) }}
              </span>
            </div>
          </div>
        </NuxtLink>
      </section>
    </template>

    <p v-if="visible.length && visible.length !== vendors.length" class="mt-6 text-sm opacity-60">
      Showing {{ visible.length }} of {{ vendors.length }} services.
    </p>
  </div>
</template>
