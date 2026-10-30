<script setup lang="ts">
import type { ComponentStatus, VendorDetail } from '~/types/api'

const props = defineProps<{ vendor: VendorDetail; ownerId?: number }>()

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

// The parts of the page are tabs rather than one long scroll, and the open tab
// is part of the address. The public tabs can be read from the query on the
// server; the log is staff and owner only, so it joins after the session is
// known and is never part of the cached page.
type PublicTab = 'components' | 'incidents' | 'recent'
type Tab = PublicTab | 'logs'

const { user, canManageServices } = useAuth()
const tabGroup = useId()

const publicTabs = computed<{ value: PublicTab; label: string }[]>(() => [
  { value: 'components', label: 'Components' },
  { value: 'incidents', label: 'Incidents' },
  { value: 'recent', label: 'Recent status' },
])

function isPublicTab(value: string): value is PublicTab {
  return publicTabs.value.some((tab) => tab.value === value)
}

function requestedTab(): Tab {
  const wanted = String(route.query.tab ?? '')
  return isPublicTab(wanted) ? wanted : 'components'
}

const tab = ref<Tab>(requestedTab())

function selectTab(next: Tab) {
  tab.value = next
  const query = { ...route.query }
  if (next === 'components') {
    delete query.tab
  } else {
    query.tab = next
  }
  router.replace({ query })
}

const showLogs = computed(
  () => canManageServices.value || (props.ownerId != null && props.ownerId === user.value?.id),
)

function syncTabFromQuery() {
  const wanted = String(route.query.tab ?? '')
  if (wanted === 'logs') {
    tab.value = showLogs.value ? 'logs' : 'components'
    return
  }
  tab.value = isPublicTab(wanted) ? wanted : 'components'
}

watch(() => route.query.tab, syncTabFromQuery)

watch(showLogs, (allowed) => {
  if (!allowed && tab.value === 'logs') {
    selectTab('components')
  } else if (allowed && String(route.query.tab ?? '') === 'logs') {
    tab.value = 'logs'
  }
})

onMounted(() => {
  if (showLogs.value && String(route.query.tab ?? '') === 'logs') {
    tab.value = 'logs'
  }
})

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
          Generated <LocalTime :at="vendor.generatedSummary.generatedAt" />
        </span>
      </div>
      <p class="mt-4 text-[15px] leading-7 text-base-content/85">
        {{ vendor.generatedSummary.text }}
      </p>
    </section>

    <details
      v-if="openIncidents.length"
      class="collapse collapse-arrow mb-6 rounded-box border border-base-300 bg-base-100 shadow-sm"
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

    <div class="tabs tabs-lift w-full" role="tablist" aria-label="Service details">
      <template v-for="t in publicTabs" :key="t.value">
        <label
          class="tab flex-none cursor-pointer whitespace-nowrap"
          :class="{ 'tab-active': tab === t.value }"
        >
          <input
            type="radio"
            class="sr-only"
            :name="tabGroup"
            :checked="tab === t.value"
            @change="selectTab(t.value)"
          />
          {{ t.label }}
        </label>
        <div class="tab-content basis-full bg-base-100 border-base-300 p-6">
          <section v-if="t.value === 'components' && tab === 'components'">
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

          <section v-else-if="t.value === 'incidents' && tab === 'incidents'">
            <h2 class="mb-3 text-lg font-semibold">Incidents</h2>

            <ul v-if="incidents.length" class="timeline timeline-vertical">
              <li
                v-for="(i, idx) in incidents"
                :key="i.sourceId"
                class="[--timeline-col-start:4.5rem] [--timeline-col-end:1fr]"
              >
                <hr v-if="idx > 0" />
                <div class="timeline-start justify-self-start whitespace-nowrap text-xs opacity-60">
                  <LiveAgo :at="i.startedAt" long />
                </div>
                <div class="timeline-middle">
                  <svg
                    v-if="i.resolvedAt"
                    xmlns="http://www.w3.org/2000/svg"
                    viewBox="0 0 20 20"
                    fill="currentColor"
                    class="h-5 w-5 text-success"
                    aria-hidden="true"
                  >
                    <path
                      fill-rule="evenodd"
                      d="M10 18a8 8 0 100-16 8 8 0 000 16zm3.857-9.809a.75.75 0 00-1.214-.882l-3.483 4.79-1.88-1.88a.75.75 0 10-1.06 1.061l2.5 2.5a.75.75 0 001.137-.089l4-5.5z"
                      clip-rule="evenodd"
                    />
                  </svg>
                  <svg
                    v-else
                    xmlns="http://www.w3.org/2000/svg"
                    viewBox="0 0 20 20"
                    fill="currentColor"
                    class="h-5 w-5 text-error"
                    aria-hidden="true"
                  >
                    <path
                      fill-rule="evenodd"
                      d="M18 10a8 8 0 11-16 0 8 8 0 0116 0zm-8-5a.75.75 0 01.75.75v4.5a.75.75 0 01-1.5 0v-4.5A.75.75 0 0110 5zm0 10a1 1 0 100-2 1 1 0 000 2z"
                      clip-rule="evenodd"
                    />
                  </svg>
                </div>
                <div class="timeline-end timeline-box mb-3 w-full">
                  <div class="flex flex-wrap items-center gap-2">
                    <span class="font-medium">{{ i.name }}</span>
                    <span class="badge badge-ghost badge-sm">{{ i.status }}</span>
                    <span v-if="i.impact" class="badge badge-sm">{{ i.impact }}</span>
                  </div>
                  <p class="mt-1 text-xs opacity-60">
                    <template v-if="i.resolvedAt">
                      Resolved <LiveAgo :at="i.resolvedAt" long />
                    </template>
                    <template v-else>Ongoing</template>
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
            <p v-else class="text-sm opacity-60">No incidents reported.</p>
          </section>

          <section v-else-if="t.value === 'recent' && tab === 'recent'">
            <h2 class="mb-3 text-lg font-semibold">Recent status changes</h2>
            <ul v-if="events.length" class="timeline timeline-vertical">
              <li
                v-for="(e, idx) in events"
                :key="idx"
                class="[--timeline-col-start:4.5rem] [--timeline-col-end:1fr]"
              >
                <hr v-if="idx > 0" />
                <div class="timeline-start justify-self-start whitespace-nowrap text-xs opacity-60">
                  <LiveAgo :at="e.changedAt" long />
                </div>
                <div class="timeline-middle">
                  <svg
                    xmlns="http://www.w3.org/2000/svg"
                    viewBox="0 0 20 20"
                    fill="currentColor"
                    class="h-5 w-5 text-base-content/30"
                    aria-hidden="true"
                  >
                    <path
                      fill-rule="evenodd"
                      d="M10 18a8 8 0 100-16 8 8 0 000 16zm3.857-9.809a.75.75 0 00-1.214-.882l-3.483 4.79-1.88-1.88a.75.75 0 10-1.06 1.061l2.5 2.5a.75.75 0 001.137-.089l4-5.5z"
                      clip-rule="evenodd"
                    />
                  </svg>
                </div>
                <div class="timeline-end timeline-box mb-2 w-full">
                  <div class="flex flex-wrap items-center gap-2">
                    <StatusBadge :status="e.status" size="sm" />
                    <span v-if="e.component" class="text-sm opacity-80">{{ e.component }}</span>
                  </div>
                </div>
                <hr />
              </li>
            </ul>
            <p v-else class="text-sm opacity-60">No status changes recorded yet.</p>
          </section>
        </div>
      </template>

      <ClientOnly>
        <template v-if="showLogs">
          <label
            class="tab flex-none cursor-pointer whitespace-nowrap"
            :class="{ 'tab-active': tab === 'logs' }"
          >
            <input
              type="radio"
              class="sr-only"
              :name="tabGroup"
              :checked="tab === 'logs'"
              @change="selectTab('logs')"
            />
            Logs
          </label>
          <div class="tab-content basis-full bg-base-100 border-base-300 p-6">
            <section v-if="tab === 'logs'">
              <FetchLog :slug="vendor.slug" :owner-id="ownerId" flush />
            </section>
          </div>
        </template>
      </ClientOnly>
    </div>

    <p v-if="vendor.lastPolledAt" class="mt-8 text-xs opacity-50">
      Last checked <LiveAgo :at="vendor.lastPolledAt" />.
      <template v-if="vendor.paused">Polling is paused, so this is the last known state.</template>
      <template v-else>Data is polled every few minutes.</template>
    </p>
  </div>
</template>
