<script setup lang="ts">
import type { StaffAlert } from '~/types/api'

definePageMeta({ middleware: 'staff', ssr: false })

useSeoMeta({ title: 'All alerts', robots: 'noindex, nofollow' })

const alerts = ref<StaffAlert[]>([])
const loading = ref(true)
const error = ref('')

onMounted(async () => {
  try {
    const res = await $fetch<{ alerts: StaffAlert[]; count: number }>('/api/v1/staff/alerts')
    alerts.value = res.alerts
  } catch (err) {
    error.value = apiError(err, 'Could not load the alerts.')
  } finally {
    loading.value = false
  }

  await nextTick()
  showLinkedAlert()
})

const STATE: Record<StaffAlert['state'], { label: string; cls: string }> = {
  active: { label: 'Subscribed', cls: 'badge-success' },
  paused: { label: 'Paused', cls: 'badge-warning' },
  unsubscribed: { label: 'Unsubscribed', cls: 'badge-ghost' },
}

function stateOf(alert: StaffAlert) {
  return STATE[alert.state] ?? STATE.unsubscribed
}

// What a row watches, which is not always the vendor as a whole: an alert can
// watch the vendor's own status, some of its components, or both.
function watches(alert: StaffAlert) {
  const parts = alert.wholeService ? ['The whole service'] : []
  parts.push(...(alert.components ?? []))
  return parts.join(' · ') || 'The whole service'
}

// What a row arrives by. The destination is not part of this list: this is a
// support view, not a place to read somebody's address or webhook URL.
const CHANNEL_LABELS: Record<string, string> = {
  email: 'Email',
  webhook: 'Webhook',
  telegram: 'Telegram',
  slack: 'Slack',
  discord: 'Discord',
  lark: 'Lark',
}

function channelNames(alert: StaffAlert) {
  return (alert.channels ?? []).map((kind) => CHANNEL_LABELS[kind] ?? kind)
}

function arrives(alert: StaffAlert) {
  return channelNames(alert).join(' · ')
}

const route = useRoute()
const router = useRouter()

// The filters and the sort live in the query string, so a view can be shared,
// bookmarked, or come back with the back button.
type RowState = 'all' | StaffAlert['state']

const rowStates: { value: RowState; label: string }[] = [
  { value: 'all', label: 'All states' },
  { value: 'active', label: STATE.active.label },
  { value: 'paused', label: STATE.paused.label },
  { value: 'unsubscribed', label: STATE.unsubscribed.label },
]

const requestedState = String(route.query.state ?? 'all') as RowState
const stateFilter = ref<RowState>(
  rowStates.some((state) => state.value === requestedState) ? requestedState : 'all',
)

const requestedChannel = String(route.query.channel ?? '')
const channelFilter = ref(requestedChannel in CHANNEL_LABELS ? requestedChannel : '')

// The input keeps its own value and the URL follows a beat later, so typing does
// not write an address per keystroke.
const searchInput = ref(String(route.query.q ?? ''))
const search = ref(searchInput.value.trim())

const filtered = computed(() => {
  const needle = search.value.trim().toLowerCase()
  return alerts.value.filter((alert) => {
    if (channelFilter.value && !(alert.channels ?? []).includes(channelFilter.value)) return false
    if (stateFilter.value !== 'all' && alert.state !== stateFilter.value) return false
    if (!needle) return true
    return (
      alert.subscriberName.toLowerCase().includes(needle) ||
      alert.subscriberEmail.toLowerCase().includes(needle) ||
      alert.vendorName.toLowerCase().includes(needle)
    )
  })
})

const channelOptions = Object.entries(CHANNEL_LABELS).map(([value, label]) => ({ value, label }))

// Sorting happens here rather than in the query, because every row is already in
// the page and a click should not wait for a round trip. A header sorts by itself
// first, and clicking the same one again turns it around. Until a header is
// clicked the list keeps the order the API sent, which is the newest first.
type SortKey = 'subscriber' | 'service' | 'arrives' | 'added' | 'state'

const sortColumns: { key: SortKey; label: string }[] = [
  { key: 'subscriber', label: 'Subscriber' },
  { key: 'service', label: 'Service' },
  { key: 'arrives', label: 'Arrives by' },
  { key: 'added', label: 'Added' },
  { key: 'state', label: 'State' },
]

// ?sort=subscriber&dir=desc. A key nobody knows counts as no key at all, so a
// hand-edited URL lands on the default order rather than an empty list.
function requestedSort(): SortKey | '' {
  const key = String(route.query.sort ?? '')
  return sortColumns.some((column) => column.key === key) ? (key as SortKey) : ''
}

const sortKey = ref<SortKey | ''>(requestedSort())
const sortAsc = ref(route.query.dir !== 'desc')

function sortBy(key: SortKey) {
  if (sortKey.value === key) {
    sortAsc.value = !sortAsc.value
    return
  }
  sortKey.value = key
  sortAsc.value = true
}

function sortField(alert: StaffAlert, key: SortKey): string | number {
  switch (key) {
    case 'subscriber':
      return `${alert.subscriberName} ${alert.subscriberEmail}`
    case 'service':
      return alert.vendorName
    case 'arrives':
      return arrives(alert)
    case 'added':
      return Date.parse(alert.createdAt)
    // The state words sort into the order the filter names them, which is what
    // arriving, held back and ended are.
    case 'state':
      return alert.state
  }
}

const sorted = computed(() => {
  const key = sortKey.value
  if (!key) return filtered.value

  const direction = sortAsc.value ? 1 : -1
  return [...filtered.value].sort((a, b) => {
    const left = sortField(a, key)
    const right = sortField(b, key)
    if (typeof left === 'number' && typeof right === 'number') {
      return (left - right) * direction
    }
    return String(left).localeCompare(String(right)) * direction
  })
})

// Paging is here too, beside the sorting, so turning a page does not go back to
// the API. It is in the address as well, so a page can be linked to.
const PAGE_SIZE = 25

// ?page=2. A page nobody could be on, such as a word or a negative number, is
// read as the first one, the way an unknown sort key is read as no key.
function requestedPage(): number {
  const raw = Number(route.query.page)
  return Number.isFinite(raw) && raw >= 1 ? Math.trunc(raw) : 1
}

const page = ref(requestedPage())

const pageCount = computed(() => Math.max(1, Math.ceil(sorted.value.length / PAGE_SIZE)))

// A page can lose its rows from under it, when a filter narrows or an alert ends.
// Reading it clamped, rather than leaving it pointing past the end, keeps the
// list on screen.
const currentPage = computed(() => Math.min(page.value, pageCount.value))

const paged = computed(() =>
  sorted.value.slice((currentPage.value - 1) * PAGE_SIZE, currentPage.value * PAGE_SIZE),
)

// A notification links to one alert. Both the table and the cards are in the
// document for the responsive layout, so the hash is resolved to the one that
// is visible, and the page holding it is opened first.
function showLinkedAlert() {
  const prefix = '#alert-'
  if (!route.hash.startsWith(prefix)) return

  const id = Number(route.hash.slice(prefix.length))
  const index = sorted.value.findIndex((alert) => alert.id === id)
  if (index < 0) return

  page.value = Math.floor(index / PAGE_SIZE) + 1
  nextTick(() => {
    const nodes = document.querySelectorAll<HTMLElement>(`[data-alert-id="${id}"]`)
    for (const node of nodes) {
      if (node.offsetParent !== null) {
        node.scrollIntoView({ block: 'start' })
        return
      }
    }
  })
}

const firstRow = computed(() => (sorted.value.length ? (currentPage.value - 1) * PAGE_SIZE + 1 : 0))
const lastRow = computed(() => Math.min(currentPage.value * PAGE_SIZE, sorted.value.length))

// A filter, a search or a sort is a new list, so it starts at the top.
watch([channelFilter, stateFilter, search, sortKey, sortAsc], () => {
  page.value = 1
})

// What a header says about the sort, to a reader and to a screen reader alike.
function sortState(key: SortKey) {
  if (sortKey.value !== key) return 'none'
  return sortAsc.value ? 'ascending' : 'descending'
}

// The drawer the filters live in: a side panel from md up, and the same panel
// rising from the bottom on a phone, the way the catalogue's filter works.
const filtersOpen = ref(false)
const filterTrigger = ref<HTMLButtonElement | null>(null)
const filterPanel = ref<HTMLElement | null>(null)

const activeFilterCount = computed(
  () =>
    (channelFilter.value !== '' ? 1 : 0) +
    (stateFilter.value !== 'all' ? 1 : 0),
)

function clearFilters() {
  channelFilter.value = ''
  stateFilter.value = 'all'
}

watch(filtersOpen, async (isOpen) => {
  if (isOpen) {
    await nextTick()
    filterPanel.value?.focus()
    return
  }
  filterTrigger.value?.focus()
})

// Everything Clear would put back: a filter or an order.
const clearable = computed(() => activeFilterCount.value > 0 || sortKey.value !== '')

// Clear puts the list back to what the page opens with. The search box is left
// alone, because a term somebody typed is their own to remove.
function clearList() {
  clearFilters()
  sortKey.value = ''
  sortAsc.value = true
}

// The URL follows the page, so the view on screen is the one that can be linked
// to. Only the search term is followed the other way, because a shared link is
// how a search usually arrives.
function syncUrl() {
  const query: Record<string, string> = {}
  if (channelFilter.value) query.channel = channelFilter.value
  if (stateFilter.value !== 'all') query.state = stateFilter.value
  if (search.value) query.q = search.value
  if (sortKey.value) {
    query.sort = sortKey.value
    if (!sortAsc.value) query.dir = 'desc'
  }
  if (currentPage.value > 1) query.page = String(currentPage.value)
  router.replace({ query })
}

let searchTimer: ReturnType<typeof setTimeout> | undefined

watch(searchInput, (value) => {
  clearTimeout(searchTimer)
  searchTimer = setTimeout(() => {
    search.value = value.trim()
    syncUrl()
  }, 250)
})

onBeforeUnmount(() => clearTimeout(searchTimer))

watch([channelFilter, stateFilter, sortKey, sortAsc, currentPage], syncUrl)

// Follow the URL when it changes from somewhere else, such as the back button.
watch(
  () => route.query.q,
  (value) => {
    const next = String(value ?? '')
    if (next !== searchInput.value.trim()) {
      searchInput.value = next
      search.value = next
    }
  },
)
</script>

<template>
  <div class="mx-auto max-w-6xl px-4 py-8">
    <header class="mb-6">
      <h1 class="text-2xl font-bold tracking-tight">All alerts</h1>
      <p class="mt-1 text-sm opacity-70">
        Every alert, including the ones guests left without an account. This view
        is read only: pausing, editing and deleting stay with the subscriber.
      </p>
    </header>

    <p v-if="error" role="alert" class="alert alert-error mb-6">
      <span>{{ error }}</span>
    </p>

    <div v-if="loading" class="flex justify-center py-16">
      <span class="loading loading-spinner" />
    </div>

    <p
      v-else-if="!alerts.length"
      class="rounded-box border border-base-300 bg-base-100 p-6 text-center text-sm opacity-70 shadow-sm"
    >
      No account has an alert yet.
    </p>

    <template v-else>
      <div class="mb-4 flex flex-wrap items-center gap-3">
        <input
          v-model="searchInput"
          type="search"
          class="input input-bordered w-full sm:max-w-sm"
          placeholder="Search by subscriber or service"
          aria-label="Search alerts"
        />

        <button
          ref="filterTrigger"
          type="button"
          class="btn h-10 min-h-10 border-base-300 bg-base-100"
          aria-haspopup="dialog"
          aria-controls="staff-alert-filter"
          :aria-expanded="filtersOpen"
          @click="filtersOpen = true"
        >
          <svg
            xmlns="http://www.w3.org/2000/svg"
            class="h-4 w-4"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="2"
            stroke-linecap="round"
            stroke-linejoin="round"
            aria-hidden="true"
          >
            <path d="M4 6h16M7 12h10M10 18h4" />
          </svg>
          Filter
          <span v-if="activeFilterCount" class="badge badge-primary badge-sm">
            {{ activeFilterCount }}
          </span>
        </button>

        <span v-if="search || clearable" class="text-xs opacity-60">
          {{ filtered.length }} match{{ filtered.length === 1 ? '' : 'es' }}
        </span>
        <button v-if="clearable" type="button" class="link text-xs" @click="clearList">
          Clear
        </button>
      </div>

      <!-- A phone cannot hold five columns without scrolling sideways, so the
           table is for wide screens and the same rows are cards below. -->
      <div class="hidden overflow-x-auto rounded-box border border-base-300 bg-base-100 shadow-sm md:block">
        <table class="table">
          <thead>
            <tr>
              <th v-for="column in sortColumns" :key="column.key" :aria-sort="sortState(column.key)">
                <button type="button" class="cursor-pointer whitespace-nowrap" @click="sortBy(column.key)">
                  {{ column.label }}
                  <span v-if="sortKey === column.key" aria-hidden="true">
                    {{ sortAsc ? '↑' : '↓' }}
                  </span>
                </button>
              </th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="alert in paged"
              :data-alert-id="alert.id"
              :key="alert.id"
              class="scroll-mt-20 align-top"
            >
              <td>
                <div class="font-medium">{{ alert.guest ? 'Guest' : alert.subscriberName }}</div>
                <div class="text-xs break-all opacity-60">{{ alert.subscriberEmail }}</div>
              </td>
              <td>
                <NuxtLink :to="`/${alert.vendorSlug}`" class="link font-medium">
                  {{ alert.vendorName }}
                </NuxtLink>
                <div class="mt-1">
                  <StatusBadge :status="alert.vendorStatus" size="xs" />
                </div>
                <p class="mt-1 text-xs opacity-70">{{ watches(alert) }}</p>
              </td>
              <td class="text-sm whitespace-nowrap opacity-70">{{ arrives(alert) }}</td>
              <td class="text-sm whitespace-nowrap opacity-70">{{ formatDate(alert.createdAt) }}</td>
              <td>
                <span class="badge badge-sm whitespace-nowrap" :class="stateOf(alert).cls">
                  {{ stateOf(alert).label }}
                </span>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <ul class="flex flex-col gap-3 md:hidden">
        <li
          v-for="alert in paged"
          :data-alert-id="alert.id"
          :key="alert.id"
          class="scroll-mt-20 rounded-box border border-base-300 bg-base-100 p-4 shadow-sm"
        >
          <div class="flex items-start justify-between gap-3">
            <div class="min-w-0">
              <div class="font-medium">{{ alert.guest ? 'Guest' : alert.subscriberName }}</div>
              <div class="text-xs break-all opacity-60">{{ alert.subscriberEmail }}</div>
            </div>
            <span class="badge badge-sm shrink-0 whitespace-nowrap" :class="stateOf(alert).cls">
              {{ stateOf(alert).label }}
            </span>
          </div>

          <div class="mt-3">
            <NuxtLink :to="`/${alert.vendorSlug}`" class="link font-medium">
              {{ alert.vendorName }}
            </NuxtLink>
            <div class="mt-1">
              <StatusBadge :status="alert.vendorStatus" size="xs" />
            </div>
            <p class="mt-1 text-xs opacity-70">{{ watches(alert) }}</p>
          </div>

          <p class="mt-2 text-sm opacity-70">{{ arrives(alert) }}</p>
          <p class="mt-1 text-xs opacity-60">added {{ formatDate(alert.createdAt) }}</p>
        </li>
      </ul>

      <div v-if="sorted.length" class="mt-4 flex flex-wrap items-center justify-between gap-3">
        <p class="text-xs opacity-60">
          Showing {{ firstRow }}&ndash;{{ lastRow }} of {{ sorted.length }}
        </p>
        <div v-if="pageCount > 1" class="join">
          <button
            type="button"
            class="btn btn-sm join-item"
            :disabled="currentPage === 1"
            @click="page = currentPage - 1"
          >
            Previous
          </button>
          <button type="button" class="btn btn-sm join-item" disabled>
            Page {{ currentPage }} of {{ pageCount }}
          </button>
          <button
            type="button"
            class="btn btn-sm join-item"
            :disabled="currentPage === pageCount"
            @click="page = currentPage + 1"
          >
            Next
          </button>
        </div>
      </div>

      <p
        v-if="!filtered.length"
        class="rounded-box border border-base-300 bg-base-100 p-6 text-center text-sm opacity-70 shadow-sm"
      >
        <template v-if="search">Nothing matches &ldquo;{{ search }}&rdquo;.</template>
        <template v-else>Nothing here for those filters.</template>
      </p>
    </template>

    <!-- Filters apply as they are picked, so the footer count is the list
         underneath the panel already narrowed. The panel is a drawer on a wide
         screen and rises from the bottom on a phone, the same shape the
         catalogue's filter takes. -->
    <Teleport to="body">
      <div
        v-if="filtersOpen"
        class="fixed inset-0 z-50 flex items-end justify-center bg-black/50 md:items-stretch md:justify-end"
        role="dialog"
        aria-modal="true"
        aria-labelledby="staff-alert-filter-title"
        @click.self="filtersOpen = false"
        @keydown.esc.stop.prevent="filtersOpen = false"
      >
        <div
          id="staff-alert-filter"
          ref="filterPanel"
          tabindex="-1"
          class="flex max-h-[85vh] w-full max-w-md flex-col overflow-hidden rounded-t-box border border-base-300 bg-base-100 shadow-xl outline-none md:h-full md:max-h-none md:w-96 md:rounded-none md:rounded-l-box"
        >
          <div class="mx-auto mt-2 h-1 w-10 rounded-full bg-base-300 md:hidden" />

          <div class="flex items-center justify-between border-b border-base-300 px-5 py-3">
            <h2 id="staff-alert-filter-title" class="text-lg font-semibold">Filter alerts</h2>
            <button
              type="button"
              class="btn btn-ghost btn-sm"
              aria-label="Close"
              @click="filtersOpen = false"
            >
              ✕
            </button>
          </div>

          <div class="min-h-0 flex-1 space-y-5 overflow-y-auto px-5 py-4">
            <label class="flex flex-col gap-1 text-sm">
              <span class="font-medium">Arrives by</span>
              <select v-model="channelFilter" class="select select-bordered w-full">
                <option value="">All ways</option>
                <option
                  v-for="channel in channelOptions"
                  :key="channel.value"
                  :value="channel.value"
                >
                  {{ channel.label }}
                </option>
              </select>
            </label>

            <label class="flex flex-col gap-1 text-sm">
              <span class="font-medium">State</span>
              <select v-model="stateFilter" class="select select-bordered w-full">
                <option v-for="state in rowStates" :key="state.value" :value="state.value">
                  {{ state.label }}
                </option>
              </select>
            </label>
          </div>

          <div class="flex items-center justify-between gap-3 border-t border-base-300 px-5 py-3">
            <button
              type="button"
              class="btn btn-ghost btn-sm"
              :disabled="!activeFilterCount"
              @click="clearFilters"
            >
              Reset
            </button>
            <button type="button" class="btn btn-primary btn-sm" @click="filtersOpen = false">
              Show {{ filtered.length }} alert{{ filtered.length === 1 ? '' : 's' }}
            </button>
          </div>
        </div>
      </div>
    </Teleport>
  </div>
</template>
