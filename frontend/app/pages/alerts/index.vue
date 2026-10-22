<script setup lang="ts">
import type { AccountAlert } from '~/types/api'

definePageMeta({ middleware: 'authenticated', ssr: false })

useSeoMeta({ title: 'Alerts', robots: 'noindex, nofollow' })

const alerts = ref<AccountAlert[]>([])
// How full the account is: the cap the API would hold it to (none for staff),
// and how many alerts are already counting against it.
const limit = ref<number | null>(null)
const used = ref(0)
const loading = ref(true)
const error = ref('')
const notice = ref('')
const busy = ref<number | null>(null)
const deleting = ref<AccountAlert | null>(null)

const atLimit = computed(() => limit.value !== null && used.value >= limit.value)

onMounted(async () => {
  await load()
  showSlackFailure()
})

async function load() {
  try {
    const res = await $fetch<{ alerts: AccountAlert[]; limit: number | null; used: number }>(
      '/api/v1/auth/alerts',
    )
    alerts.value = res.alerts
    limit.value = res.limit
    used.value = res.used
  } catch (err) {
    error.value = apiError(err, 'Could not load your alerts.')
  } finally {
    loading.value = false
  }
}

// A Slack install that never made it back to an alert page ends here, because
// the callback had no alert to name. The reason is read once and taken out of
// the address, so a reload does not say it again.
function showSlackFailure() {
  if (String(route.query.slack ?? '') !== 'failed') return

  notice.value = 'Slack could not be connected. Open the alert page and try again.'
  const query = { ...route.query }
  delete query.slack
  router.replace({ query })
}

// Pause, resume and unsubscribe are the same change asked for three ways.
async function setState(alert: AccountAlert, state: AccountAlert['state']) {
  busy.value = alert.id
  error.value = ''

  try {
    await $fetch(`/api/v1/auth/alerts/${alert.id}`, { method: 'PATCH', body: { state } })
    await load()
  } catch (err) {
    error.value = apiError(err)
  } finally {
    busy.value = null
  }
}

async function remove(alert: AccountAlert) {
  busy.value = alert.id
  error.value = ''

  try {
    await $fetch(`/api/v1/auth/alerts/${alert.id}`, { method: 'DELETE' })
    deleting.value = null
    await load()
  } catch (err) {
    error.value = apiError(err)
  } finally {
    busy.value = null
  }
}

const STATE: Record<AccountAlert['state'], { label: string; cls: string }> = {
  active: { label: 'Subscribed', cls: 'badge-success' },
  paused: { label: 'Paused', cls: 'badge-warning' },
  unsubscribed: { label: 'Unsubscribed', cls: 'badge-ghost' },
}

function stateOf(alert: AccountAlert) {
  return STATE[alert.state] ?? STATE.unsubscribed
}

// What a row watches, which is not always the vendor as a whole: the bell lets
// the components of a status page be ticked off one by one, and the vendor's own
// status can be watched alongside them.
function watches(alert: AccountAlert) {
  const parts = alert.wholeService ? ['The whole service'] : []
  parts.push(...(alert.components ?? []))
  return parts.join(' · ') || 'The whole service'
}

// What a row arrives by, which is what the list says about where an alert goes:
// a row can use an address, a webhook, a chat, a Slack channel, or several of
// them, and a row that is quiet or ended still says how it used to arrive. The
// destination itself is on the page for its service, beside the field that holds
// it.
const CHANNEL_LABELS: Record<string, string> = {
  email: 'Email',
  webhook: 'Webhook',
  telegram: 'Telegram',
  slack: 'Slack',
}

function channelNames(alert: AccountAlert) {
  return (alert.channels ?? []).map((kind) => CHANNEL_LABELS[kind] ?? kind)
}

function arrives(alert: AccountAlert) {
  return channelNames(alert).join(' · ')
}

// What stops arriving when the row goes. A row that watches components is about
// those rather than about the vendor, so the confirmation says which.
function deleteMessage(alert: AccountAlert) {
  const components = alert.components ?? []
  const about =
    components.length && !alert.wholeService
      ? `${components.join(', ')} on ${alert.vendorName}`
      : alert.vendorName
  return `You will not get anything about ${about} any more, and the row disappears from this list.`
}

// The controls around the table and the cards: a search, the health of the
// services being watched, the state of the rows themselves, a sort and paging.
// Each of them lives in the query string, so a view can be shared, bookmarked, or
// come back with the back button, the way the services and the users screens keep
// theirs.
const route = useRoute()
const router = useRouter()

// The state of a row is a different question from the health of the service it
// watches. A row is what an account asked to be told about, and it can be
// arriving, held back or ended. Ended is the third of those rather than something
// this screen hides, because a row that ended can be started again from here.
type RowState = 'all' | AccountAlert['state']

const rowStates: { value: RowState; label: string }[] = [
  { value: 'all', label: 'All' },
  { value: 'active', label: STATE.active.label },
  { value: 'paused', label: STATE.paused.label },
  { value: 'unsubscribed', label: STATE.unsubscribed.label },
]

// ?filter=paused. A state nobody knows counts as no filter at all, so a
// hand-edited URL lands on every row rather than on an empty list.
function requestedRowState(): RowState {
  const raw = String(route.query.filter ?? 'all')
  return rowStates.some((s) => s.value === raw) ? (raw as RowState) : 'all'
}

const rowState = ref<RowState>(requestedRowState())

// ?state=degraded,partial. Several can be picked at once, so states arrive as a
// comma separated list, and one nobody knows is dropped.
const statusFilter = ref(
  String(route.query.state ?? '')
    .split(',')
    .map((state) => state.trim())
    .filter((state) => HEALTH_STATES.includes(state)),
)

// The input keeps its own value and the URL follows a beat later, so typing does
// not write an address per keystroke.
const searchInput = ref(String(route.query.q ?? ''))
const search = ref(searchInput.value.trim())

// The search and the row state narrow first, so a chip carries what the cards
// under it would be.
const matched = computed(() => {
  const needle = search.value.trim().toLowerCase()
  return alerts.value.filter((alert) => {
    if (rowState.value !== 'all' && alert.state !== rowState.value) return false
    if (!needle) return true
    return (
      alert.vendorName.toLowerCase().includes(needle) ||
      alert.vendorSlug.includes(needle) ||
      alert.email.toLowerCase().includes(needle) ||
      channelNames(alert).some((name) => name.toLowerCase().includes(needle)) ||
      (alert.components ?? []).some((name) => name.toLowerCase().includes(needle))
    )
  })
})

const statusOptions = computed(() =>
  statusCounts(matched.value, (alert) => alert.vendorStatus, statusFilter.value),
)

const filtered = computed(() => {
  if (!statusFilter.value.length) return matched.value
  return matched.value.filter((alert) => statusFilter.value.includes(alert.vendorStatus))
})

// Sorting happens here rather than in the query, because every row is already in
// the page and a click should not wait for a round trip. A header sorts by itself
// first, and clicking the same one again turns it around. Until a header is
// clicked the list keeps the order the API sent, which is the newest first.
type SortKey = 'service' | 'arrives' | 'added' | 'state'

// The columns a reader compares, in the order the table shows them. The actions
// are read, not sorted.
const sortColumns: { key: SortKey; label: string }[] = [
  { key: 'service', label: 'Service' },
  { key: 'arrives', label: 'Arrives by' },
  { key: 'added', label: 'Added' },
  { key: 'state', label: 'State' },
]

// ?sort=service&dir=desc. A key nobody knows counts as no key at all, so a
// hand-edited URL lands on the default order rather than an empty list.
function requestedSort(): SortKey | '' {
  const key = String(route.query.sort ?? '')
  return sortColumns.some((col) => col.key === key) ? (key as SortKey) : ''
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

function sortField(alert: AccountAlert, key: SortKey): string | number {
  switch (key) {
    case 'service':
      return alert.vendorName
    case 'arrives':
      return arrives(alert)
    case 'added':
      return Date.parse(alert.createdAt)
    // The state words sort into the order the toggle names them, which is what
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

// A page can lose its rows from under it, when the search narrows or a row is
// removed. Reading it clamped, rather than leaving it pointing past the end,
// keeps the list on screen.
const currentPage = computed(() => Math.min(page.value, pageCount.value))

const paged = computed(() =>
  sorted.value.slice((currentPage.value - 1) * PAGE_SIZE, currentPage.value * PAGE_SIZE),
)

const firstRow = computed(() => (sorted.value.length ? (currentPage.value - 1) * PAGE_SIZE + 1 : 0))
const lastRow = computed(() => Math.min(currentPage.value * PAGE_SIZE, sorted.value.length))

// A filter, a search or a sort is a new list, so it starts at the top.
watch([rowState, statusFilter, search, sortKey, sortAsc], () => {
  page.value = 1
})

// What a header says about the sort, to a reader and to a screen reader alike.
function sortState(key: SortKey) {
  if (sortKey.value !== key) return 'none'
  return sortAsc.value ? 'ascending' : 'descending'
}

// Everything Clear would put back: a state, a health, or an order.
const clearable = computed(
  () => rowState.value !== 'all' || statusFilter.value.length > 0 || sortKey.value !== '',
)

// Clear puts the list back to what the page opens with: every row, every health,
// and the order the API sent. The search box is left alone, because a term
// somebody typed is their own to remove.
function clearList() {
  rowState.value = 'all'
  statusFilter.value = []
  sortKey.value = ''
  sortAsc.value = true
}

// The URL follows the page, so the view on screen is the one that can be linked
// to. Only the search term is followed the other way, because a shared link is
// how a search usually arrives. What it writes is the page on screen rather than
// the page that was asked for, so the address cannot end up pointing past the end
// of the list.
function syncUrl() {
  const query: Record<string, string> = {}
  if (rowState.value !== 'all') query.filter = rowState.value
  if (statusFilter.value.length) query.state = statusFilter.value.join(',')
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

watch([rowState, statusFilter, sortKey, sortAsc, currentPage], syncUrl)

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
      <h1 class="text-2xl font-bold tracking-tight">Alerts</h1>
      <p class="mt-1 text-sm opacity-70">
        You get an email, a webhook call, a Telegram message or a Slack message
        each time one of these changes status. An email, a chat and a Slack
        channel report on what you picked; a call carries the whole service, its
        own status and every component under it. Emails, chats and Slack messages
        carry a link that stops them as well.
      </p>
    </header>

    <p v-if="error" role="alert" class="alert alert-error mb-6">
      <span>{{ error }}</span>
    </p>

    <p v-if="notice" role="alert" class="alert alert-warning mb-6">
      <span>{{ notice }}</span>
    </p>

    <p v-if="atLimit" class="alert alert-warning mb-6">
      <span>
        Your account has reached its limit of {{ limit }} service{{ limit === 1 ? '' : 's' }}.
        Remove one before adding another.
      </span>
    </p>

    <div v-if="loading" class="flex justify-center py-16">
      <span class="loading loading-spinner" />
    </div>

    <p
      v-else-if="!alerts.length"
      class="rounded-box border border-base-300 bg-base-100 p-6 text-center text-sm opacity-70 shadow-sm"
    >
      You have no alerts yet. Open a service and use the bell on its page to add one.
    </p>

    <template v-else>
      <div class="mb-4 flex flex-wrap items-center gap-3">
        <input
          v-model="searchInput"
          type="search"
          class="input input-bordered w-full sm:max-w-sm"
          placeholder="Search by service, channel, address, or component"
          aria-label="Search alerts"
        />
        <span v-if="search" class="text-xs opacity-60">
          {{ filtered.length }} match{{ filtered.length === 1 ? '' : 'es' }}
        </span>
      </div>

      <!-- The health of the services being watched, which is a different question
           from what the row itself is doing. -->
      <StatusFilter
        v-model="statusFilter"
        :statuses="statusOptions"
        :clearable="clearable"
        label="Filter alerts by the status of the service"
        class="mb-4"
        @clear="clearList"
      />

      <!-- What the row is doing: still arriving, held back, or ended. Ended is
           here beside the other two rather than left out, since a row that
           ended can be started again from here. -->
      <div role="tablist" class="tabs tabs-box mb-4">
        <button
          v-for="s in rowStates"
          :key="s.value"
          role="tab"
          class="tab"
          :class="{ 'tab-active': rowState === s.value }"
          @click="rowState = s.value"
        >
          {{ s.label }}
        </button>
      </div>

      <!-- A phone cannot hold five columns without scrolling sideways, so the
           table is for wide screens and the same rows are cards below. -->
      <div class="hidden overflow-x-auto rounded-box border border-base-300 bg-base-100 shadow-sm md:block">
        <table class="table">
          <thead>
            <tr>
              <th v-for="col in sortColumns" :key="col.key" :aria-sort="sortState(col.key)">
                <button type="button" class="cursor-pointer whitespace-nowrap" @click="sortBy(col.key)">
                  {{ col.label }}
                  <span v-if="sortKey === col.key" aria-hidden="true">
                    {{ sortAsc ? '↑' : '↓' }}
                  </span>
                </button>
              </th>
              <th class="text-right">Actions</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="alert in paged" :key="alert.id" class="align-top">
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
              <td>
                <div class="flex justify-end">
                  <AlertActions
                    :alert="alert"
                    :busy="busy === alert.id"
                    @state="setState(alert, $event)"
                    @remove="deleting = alert"
                  />
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <ul class="flex flex-col gap-3 md:hidden">
        <li
          v-for="alert in paged"
          :key="alert.id"
          class="rounded-box border border-base-300 bg-base-100 p-4 shadow-sm"
        >
          <div class="flex items-start justify-between gap-3">
            <div class="min-w-0">
              <NuxtLink :to="`/${alert.vendorSlug}`" class="link font-medium">
                {{ alert.vendorName }}
              </NuxtLink>
              <div class="mt-1">
                <StatusBadge :status="alert.vendorStatus" size="xs" />
              </div>
              <p class="mt-1 text-xs opacity-70">{{ watches(alert) }}</p>
            </div>
            <span class="badge badge-sm shrink-0 whitespace-nowrap" :class="stateOf(alert).cls">
              {{ stateOf(alert).label }}
            </span>
          </div>

          <p class="mt-2 text-sm opacity-70">{{ arrives(alert) }}</p>
          <p class="mt-1 text-xs opacity-60">added {{ formatDate(alert.createdAt) }}</p>

          <div class="mt-3">
            <AlertActions
              :alert="alert"
              :busy="busy === alert.id"
              @state="setState(alert, $event)"
              @remove="deleting = alert"
            />
          </div>
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

    <ConfirmDialog
      v-if="deleting"
      heading="Delete this alert?"
      :message="deleteMessage(deleting)"
      confirm-label="Delete"
      :busy="busy === deleting.id"
      :error="error"
      @confirm="remove(deleting)"
      @cancel="deleting = null"
    />
  </div>
</template>
