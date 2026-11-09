<script setup lang="ts">
import type { StaffProfile } from '~/types/api'

definePageMeta({ middleware: 'staff', ssr: false })

useSeoMeta({ title: 'Profiles', robots: 'noindex, nofollow' })

const profiles = ref<StaffProfile[]>([])
const loading = ref(true)
const error = ref('')

type SortKey = 'name' | 'services'
const sortKey = ref<SortKey>('name')
const sortDir = ref<'asc' | 'desc'>('asc')

// A header sorts by itself first; clicking it again turns the list around. The
// count starts at the largest, since the question it answers is what is in use.
function sortBy(key: SortKey) {
  if (sortKey.value === key) {
    sortDir.value = sortDir.value === 'asc' ? 'desc' : 'asc'
    return
  }
  sortKey.value = key
  sortDir.value = key === 'services' ? 'desc' : 'asc'
}

const sorted = computed(() => {
  const direction = sortDir.value === 'asc' ? 1 : -1
  return [...profiles.value].sort((a, b) => {
    if (sortKey.value === 'services' && a.services !== b.services) {
      return (a.services - b.services) * direction
    }
    return a.name.localeCompare(b.name) * direction
  })
})

// The intervals are in seconds; a whole number of minutes is how they are set.
function interval(seconds: number): string {
  if (!seconds) return '—'
  return seconds % 60 === 0 ? `${seconds / 60} min` : `${seconds} s`
}

onMounted(async () => {
  try {
    const res = await $fetch<{ profiles: StaffProfile[] }>('/api/v1/staff/profiles')
    profiles.value = res.profiles
  } catch (err) {
    error.value = apiError(err, 'Could not load the profiles.')
  } finally {
    loading.value = false
  }
})
</script>

<template>
  <div class="mx-auto max-w-6xl px-4 py-8">
    <header class="mb-6">
      <h1 class="text-2xl font-bold tracking-tight">Profiles</h1>
      <p class="mt-1 text-sm opacity-70">
        Every adapter profile in <code>services.yaml</code>, and how many services run on each
        one. Read-only: the file is where a profile is declared or changed.
      </p>
    </header>

    <p v-if="error" role="alert" class="alert alert-error">{{ error }}</p>
    <p v-else-if="loading" class="text-sm opacity-60">Loading…</p>

    <div
      v-else
      class="overflow-x-auto rounded-box border border-base-300 bg-base-100 shadow-sm"
    >
      <table class="table table-sm">
        <thead>
          <tr>
            <th :aria-sort="sortKey === 'name' ? (sortDir === 'asc' ? 'ascending' : 'descending') : 'none'">
              <button
                type="button"
                class="inline-flex items-center gap-1 font-semibold"
                @click="sortBy('name')"
              >
                Profile
                <span class="text-xs" aria-hidden="true">{{
                  sortKey === 'name' ? (sortDir === 'asc' ? '▲' : '▼') : ''
                }}</span>
              </button>
            </th>
            <th>Kind</th>
            <th
              class="text-right"
              :aria-sort="sortKey === 'services' ? (sortDir === 'asc' ? 'ascending' : 'descending') : 'none'"
            >
              <button
                type="button"
                class="inline-flex items-center gap-1 font-semibold"
                @click="sortBy('services')"
              >
                Services
                <span class="text-xs" aria-hidden="true">{{
                  sortKey === 'services' ? (sortDir === 'asc' ? '▲' : '▼') : ''
                }}</span>
              </button>
            </th>
            <th>Detection</th>
            <th>Picker</th>
            <th>Endpoint</th>
            <th>Poll / active</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="p in sorted" :key="p.name">
            <td class="min-w-56 align-top">
              <p class="font-medium">{{ p.name }}</p>
              <p v-if="p.description" class="mt-0.5 text-xs opacity-60">{{ p.description }}</p>
            </td>
            <td class="align-top">
              <code class="text-xs">{{ p.kind }}</code>
            </td>
            <td class="text-right align-top tabular-nums">{{ p.services }}</td>
            <td class="align-top">
              <span
                class="badge badge-sm whitespace-nowrap"
                :class="p.detect ? 'badge-success' : 'badge-ghost'"
              >
                {{ p.detect ? 'In queue' : '—' }}
              </span>
            </td>
            <td class="align-top">
              <span
                class="badge badge-sm whitespace-nowrap"
                :class="p.visible ? 'badge-ghost' : 'badge-warning'"
              >
                {{ p.visible ? 'Shown' : 'Hidden' }}
              </span>
            </td>
            <td class="align-top text-xs opacity-70">
              {{ p.endpoint || '—' }}
            </td>
            <td class="align-top text-xs opacity-70">
              {{ interval(p.pollIntervalSeconds) }} / {{ interval(p.activeIntervalSeconds) }}
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
