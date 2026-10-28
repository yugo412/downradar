<script setup lang="ts">
import type { ComponentStatus, VendorDetail } from '~/types/api'

const props = defineProps<{ vendor: VendorDetail }>()

const components = computed(() => props.vendor.componentsList ?? [])
const incidents = computed(() => props.vendor.incidents ?? [])
const events = computed(() => props.vendor.recentEvents ?? [])
const openIncidents = computed(() => incidents.value.filter((i) => !i.resolvedAt))

// A status page can report a hundred components, so the table is searchable. The
// list is already here, so the filter runs in the browser: it is instant, and the
// page stays one cached fetch that everybody gets the same copy of.
const search = ref('')
const needle = computed(() => search.value.trim().toLowerCase())
const matched = computed(() => {
  if (!needle.value) return components.value
  return components.value.filter(
    (c) =>
      c.name.toLowerCase().includes(needle.value) ||
      (c.group ?? '').toLowerCase().includes(needle.value),
  )
})

// The badge counts are counted after the search, so the number next to a state
// always agrees with the rows underneath it.
const statusFilter = ref<string[]>([])
const statusOptions = computed(() =>
  statusCounts(matched.value, (c) => c.status, statusFilter.value),
)

// A header sorts itself first in its own natural direction: names A to Z,
// states worst first, and the newest change first. The query carries the sort so
// a copied link opens the same view.
type SortKey = 'name' | 'state' | 'since'

const sortColumns: { key: SortKey; label: string }[] = [
  { key: 'name', label: 'Component' },
  { key: 'state', label: 'State' },
  { key: 'since', label: 'Since' },
]

const naturalAsc: Record<SortKey, boolean> = {
  name: true,
  state: false,
  since: false,
}

const route = useRoute()
const router = useRouter()

function isSortKey(value: string): value is SortKey {
  return sortColumns.some((column) => column.key === value)
}

function requestedSort(): { key: SortKey; asc: boolean } {
  const requested = String(route.query.sort ?? '')
  const key = isSortKey(requested) ? requested : 'name'
  return { key, asc: String(route.query.dir ?? '') !== 'desc' }
}

const initialSort = requestedSort()
const sortKey = ref<SortKey>(initialSort.key)
const sortAsc = ref(initialSort.asc)

function sortBy(key: SortKey) {
  if (sortKey.value === key) {
    sortAsc.value = !sortAsc.value
  } else {
    sortKey.value = key
    sortAsc.value = naturalAsc[key]
  }
  syncSortUrl()
}

function syncSortUrl() {
  const query = { ...route.query, sort: sortKey.value }
  if (sortAsc.value) {
    delete query.dir
  } else {
    query.dir = 'desc'
  }
  router.replace({ query })
}

watch(
  () => [route.query.sort, route.query.dir],
  () => {
    const next = requestedSort()
    sortKey.value = next.key
    sortAsc.value = next.asc
  },
)

function sortState(key: SortKey): 'none' | 'ascending' | 'descending' {
  if (sortKey.value !== key) return 'none'
  return sortAsc.value ? 'ascending' : 'descending'
}

function changedAt(component: ComponentStatus): number | null {
  if (!component.changedAt) return null
  const time = Date.parse(component.changedAt)
  return Number.isNaN(time) ? null : time
}

function compareComponents(a: ComponentStatus, b: ComponentStatus): number {
  if (sortKey.value === 'name') {
    return a.name.localeCompare(b.name) * (sortAsc.value ? 1 : -1)
  }
  if (sortKey.value === 'state') {
    const rank = severity(a.status) - severity(b.status)
    if (rank !== 0) return rank * (sortAsc.value ? 1 : -1)
    return a.name.localeCompare(b.name)
  }

  const left = changedAt(a)
  const right = changedAt(b)
  if (left === null && right === null) return a.name.localeCompare(b.name)
  if (left === null) return 1
  if (right === null) return -1
  if (left === right) return a.name.localeCompare(b.name)
  return (left - right) * (sortAsc.value ? 1 : -1)
}

const shownComponents = computed(() => {
  const rows = statusFilter.value.length
    ? matched.value.filter((c) => statusFilter.value.includes(c.status))
    : matched.value
  return [...rows].sort(compareComponents)
})
</script>

<template>
  <div>
    <section
      v-if="vendor.generatedSummary?.text"
      class="mb-8 rounded-box border border-primary/20 border-l-4 border-l-primary bg-base-100 p-5 shadow-sm"
    >
      <div class="flex flex-wrap items-start justify-between gap-3">
        <div class="flex items-center gap-3">
          <span
            class="flex size-8 shrink-0 items-center justify-center rounded-full bg-primary text-primary-content shadow-sm"
          >
            <svg
              xmlns="http://www.w3.org/2000/svg"
              class="size-4"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              stroke-width="1.5"
              stroke-linecap="round"
              stroke-linejoin="round"
              aria-hidden="true"
            >
              <path
                d="M9.813 15.904 9 18.75l-.813-2.846a4.5 4.5 0 0 0-3.09-3.09L2.25 12l2.846-.813a4.5 4.5 0 0 0 3.09-3.09L9 5.25l.813 2.846a4.5 4.5 0 0 0 3.09 3.09L15.75 12l-2.846.813a4.5 4.5 0 0 0-3.09 3.09ZM18.259 8.715 18 9.75l-.259-1.035a3.375 3.375 0 0 0-2.455-2.456L14.25 6l1.036-.259a3.375 3.375 0 0 0 2.455-2.456L18 2.25l.259 1.035a3.375 3.375 0 0 0 2.456 2.456L21.75 6l-1.035.259a3.375 3.375 0 0 0-2.456 2.456Z"
              />
            </svg>
          </span>
          <div>
            <h2 class="text-sm font-semibold">Generated summary</h2>
            <p class="text-xs opacity-60">Written from the official status data</p>
          </div>
        </div>
        <span class="text-xs tabular-nums opacity-50">
          Generated {{ formatDate(vendor.generatedSummary.generatedAt) }}
        </span>
      </div>
      <p class="mt-4 text-[15px] leading-7 text-base-content/85">
        {{ vendor.generatedSummary.text }}
      </p>
    </section>

    <details
      v-if="openIncidents.length"
      class="collapse collapse-arrow mb-8 rounded-box border border-base-300 bg-base-100 shadow-sm"
    >
      <summary class="collapse-title flex items-center gap-2 font-semibold">
        <span class="badge badge-warning badge-sm">{{ openIncidents.length }}</span>
        <span>Open incident{{ openIncidents.length > 1 ? 's' : '' }}</span>
      </summary>
      <div class="collapse-content">
        <ul class="list-disc list-inside text-sm">
          <li v-for="i in openIncidents" :key="i.sourceId">{{ i.name }}</li>
        </ul>
      </div>
    </details>

    <section class="mb-8">
      <h2 class="mb-3 text-lg font-semibold">Components</h2>

      <input
        v-if="components.length"
        v-model="search"
        type="search"
        class="input input-bordered mb-3 w-full"
        placeholder="Search components"
        aria-label="Search components"
      />

      <StatusFilter
        v-model="statusFilter"
        :statuses="statusOptions"
        label="Filter components by state"
        class="mb-3"
      />

      <div class="overflow-x-auto bg-base-100 border border-base-300 rounded-box shadow-sm">
        <table class="table">
          <thead>
            <tr>
              <th
                v-for="col in sortColumns"
                :key="col.key"
                :aria-sort="sortState(col.key)"
                :class="col.key === 'since' ? 'text-right' : ''"
              >
                <button
                  type="button"
                  class="inline-flex cursor-pointer select-none items-center gap-1 whitespace-nowrap"
                  @click="sortBy(col.key)"
                >
                  {{ col.label }}
                  <span
                    aria-hidden="true"
                    :class="sortKey === col.key ? '' : 'opacity-40'"
                  >
                    {{ sortKey === col.key ? (sortAsc ? '↑' : '↓') : '↕' }}
                  </span>
                </button>
              </th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="c in shownComponents" :key="c.sourceId">
              <td class="font-medium">{{ c.name }}</td>
              <td><StatusBadge :status="c.status" size="sm" /></td>
              <td class="whitespace-nowrap text-right text-sm opacity-60">
                <LiveAgo :at="c.changedAt" />
              </td>
            </tr>
            <tr v-if="!shownComponents.length">
              <td colspan="3" class="text-center opacity-60">
                <template v-if="!components.length">No components reported yet.</template>
                <template v-else-if="!matched.length">
                  No component matches “{{ search.trim() }}”.
                </template>
                <template v-else>No component is in the states you picked.</template>
              </td>
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
      Last checked <LiveAgo :at="vendor.lastPolledAt" />.
      <template v-if="vendor.paused">Polling is paused, so this is the last known state.</template>
      <template v-else>Data is polled every few minutes.</template>
    </p>
  </div>
</template>
