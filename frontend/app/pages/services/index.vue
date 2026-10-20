<script setup lang="ts">
import type { AdminProfile, AdminService, ServiceStatus } from '~/types/api'

definePageMeta({ middleware: 'authenticated', ssr: false })

useSeoMeta({ title: 'Services', robots: 'noindex, nofollow' })

// The stage a service is at, which is what the tabs narrow to: a suggestion
// waiting for a decision, one nothing can read, the live ones, the dead ends, and
// the rows that were taken off the site.
type Stage = 'all' | ServiceStatus | 'removed'

const stages: { value: Stage; label: string }[] = [
  { value: 'all', label: 'All' },
  { value: 'proposed', label: 'Proposed' },
  { value: 'no_match', label: 'No match' },
  { value: 'published', label: 'Published' },
  { value: 'rejected', label: 'Rejected' },
  { value: 'removed', label: 'Removed' },
]

const route = useRoute()
const router = useRouter()
const { user, canManageServices, isAdmin } = useAuth()

// Every filter and the sort live in the query string, so a view can be shared,
// bookmarked, or come back with the back button. Whatever the URL carries when
// the page opens is what the list starts with.
const requestedStage = String(route.query.filter ?? 'all') as Stage
const stage = ref<Stage>(stages.some((s) => s.value === requestedStage) ? requestedStage : 'all')

// The reported health, which is a different question from the stage: a service
// that is published can still be answering nothing. Several can be picked at
// once, so states arrive as a comma separated list.
const stateFilter = ref(
  String(route.query.state ?? '')
    .split(',')
    .map((state) => state.trim())
    .filter((state) => HEALTH_STATES.includes(state)),
)

const profile = ref(String(route.query.profile ?? ''))

// The input keeps its own value and the request is debounced, so typing does
// not fire one query per keystroke. The URL follows along, so a search is
// shareable and the back button works.
const searchInput = ref(String(route.query.q ?? ''))
const search = ref(searchInput.value.trim())

// The stage and the search go to the API. The health chips and the profile pick
// are applied here, because a chip carries how many rows are in that health,
// which cannot be counted from a list that is already narrowed to it.
const { data, pending, error, refresh } = useFetch<{ services: AdminService[]; count: number }>(
  () => `/api/v1/services?filter=${stage.value}&q=${encodeURIComponent(search.value)}`,
)

const services = computed(() => {
  const rows = data.value?.services ?? []
  // A subscriber cannot restore or purge a row once it is off the site, so it
  // leaves their list when it leaves the site. Staff still see it and decide.
  return canManageServices.value ? rows : rows.filter((service) => service.active)
})

// The adapter names, for the profile picker. Taken from the catalogue rather than
// from the rows on screen, so a profile can still be picked after a search that
// hides every service using it.
const { data: catalog } = useFetch<{ profiles: AdminProfile[] }>('/api/v1/profiles')

const profileOptions = computed(() => {
  const names = (catalog.value?.profiles ?? []).map((entry) => entry.name)
  if (profile.value && !names.includes(profile.value)) names.push(profile.value)
  return names
})

const byProfile = computed(() =>
  profile.value
    ? services.value.filter((service) => service.profile === profile.value)
    : services.value,
)

// The health chips count what the stage, the search and the profile left, so the
// number on one is what picking it would show. Worst first, and a chip that is
// already picked stays on the bar at zero so it can be picked off again.
const stateOptions = computed(() =>
  statusCounts(byProfile.value, (service) => service.state, stateFilter.value),
)

const filtered = computed(() =>
  stateFilter.value.length
    ? byProfile.value.filter((service) => stateFilter.value.includes(service.state))
    : byProfile.value,
)
const created = computed(() => String(route.query.created ?? ''))

// The query carries the slug of a service that was just added, so the notice can
// say what became of it. A vendor that no profile could read is saved as
// no_match and waits for an adapter, which is worth explaining there.
const createdService = computed(() => services.value.find((s) => s.slug === created.value) ?? null)
const createdName = computed(() => createdService.value?.name ?? created.value)
const createdParked = computed(() => createdService.value?.status === 'no_match')

// Sorting happens here rather than in the query, because every row is already in
// the page and a click should not wait for a round trip. A header sorts by itself
// first, and clicking the same one again turns it around. Until a header is
// clicked the list keeps the order the API sent, which is the one the stage tabs
// describe.
type SortKey = 'name' | 'status' | 'profile' | 'updated'

// The columns a reader compares, in the order the table shows them. The url and
// the actions are read, not sorted.
const sortColumns: { key: SortKey; label: string }[] = [
  { key: 'name', label: 'Service' },
  { key: 'status', label: 'Status' },
  { key: 'profile', label: 'Profile' },
  { key: 'updated', label: 'Updated' },
]

// ?sort=name&dir=desc. A key nobody knows counts as no key at all, so a
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

function sortField(service: AdminService, key: SortKey): string | number {
  switch (key) {
    case 'name':
      return service.name
    case 'status':
      return serviceStatusRank(service.status, service.active)
    case 'profile':
      return service.profile
    case 'updated':
      return Date.parse(service.updatedAt)
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
// the API. The count under the search stays the size of the whole result, because
// that is the question a reader asks there.
const PAGE_SIZE = 25
const page = ref(1)

const pageCount = computed(() => Math.max(1, Math.ceil(sorted.value.length / PAGE_SIZE)))

// A page can lose its rows from under it, when a service is removed or the search
// narrows. Reading it clamped, rather than leaving it pointing past the end,
// keeps the list on screen.
const currentPage = computed(() => Math.min(page.value, pageCount.value))

const paged = computed(() =>
  sorted.value.slice((currentPage.value - 1) * PAGE_SIZE, currentPage.value * PAGE_SIZE),
)

const firstRow = computed(() => (sorted.value.length ? (currentPage.value - 1) * PAGE_SIZE + 1 : 0))
const lastRow = computed(() => Math.min(currentPage.value * PAGE_SIZE, sorted.value.length))

// A filter, a profile, a search or a sort is a new list, so it starts at the top.
watch([stage, stateFilter, profile, search, sortKey, sortAsc], () => {
  page.value = 1
})

// What a header says about the sort, to a reader and to a screen reader alike.
function sortState(key: SortKey) {
  if (sortKey.value !== key) return 'none'
  return sortAsc.value ? 'ascending' : 'descending'
}

// Everything Clear would put back: a stage, a health, a profile, or an order.
const clearable = computed(
  () =>
    stage.value !== 'all' ||
    stateFilter.value.length > 0 ||
    profile.value !== '' ||
    sortKey.value !== '',
)

// Clear puts the list back to what the page opens with: every stage, every
// health, every profile, and the order the API sent. The search box is left
// alone, because a term somebody typed is their own to remove.
function clearList() {
  stage.value = 'all'
  stateFilter.value = []
  profile.value = ''
  sortKey.value = ''
  sortAsc.value = true
}

const editing = ref<AdminService | null>(null)
const removing = ref<AdminService | null>(null)
const removeBusy = ref(false)
const removeError = ref('')
const purging = ref<AdminService | null>(null)
const purgeBusy = ref(false)
const purgeError = ref('')
const actionError = ref('')
const actionNotice = ref('')
const retrying = ref('')

// The URL follows the page, so the view on screen is the one that can be linked
// to. Only the search term is followed the other way, because a shared link is
// how a search usually arrives.
function syncUrl() {
  const query: Record<string, string> = {}
  if (stage.value !== 'all') query.filter = stage.value
  if (stateFilter.value.length) query.state = stateFilter.value.join(',')
  if (profile.value) query.profile = profile.value
  if (search.value) query.q = search.value
  if (sortKey.value) {
    query.sort = sortKey.value
    if (!sortAsc.value) query.dir = 'desc'
  }
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

watch([stage, stateFilter, profile, sortKey, sortAsc], syncUrl)

// Follow the URL when it changes from somewhere else, such as the back button
// or a shared link, without fighting the typing above.
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

function ownsService(service: AdminService) {
  return Boolean(service.ownerId && service.ownerId === user.value?.id)
}

// Mirrors what the API enforces: the owner may always remove their own service,
// staff follow the catalogue rules.
function canRemove(service: AdminService) {
  if (ownsService(service)) return true
  if (service.status === 'proposed') return canManageServices.value
  return isAdmin.value
}

// A force delete is for a row that is already off the site, and it is an admin's
// alone, which is what the API says too.
function canPurge(service: AdminService) {
  return !service.active && isAdmin.value
}

// Every name in this list opens the signed in view, which is where the fetch log
// and the components are, and which exists for a row at any stage. The public
// page is one click further on from there.
function detailLink(service: AdminService) {
  return `/services/${service.slug}`
}

function removalMessage(service: AdminService | null) {
  if (!service) return ''
  if (service.status === 'proposed') {
    return `${service.name} was only a suggestion, so it is deleted for good. Nothing else is affected.`
  }
  if (!canManageServices.value) {
    return `${service.name} is taken off the site and stops being checked. Its history is kept, and staff can restore it.`
  }
  return `${service.name} is taken off the site and stops being polled. Its history is kept, and you can restore it from the Removed filter.`
}

async function confirmRemove() {
  if (!removing.value) return

  removeBusy.value = true
  removeError.value = ''

  try {
    await $fetch(`/api/v1/services/${removing.value.slug}`, { method: 'DELETE' })
    removing.value = null
    await refresh()
  } catch (err) {
    removeError.value = apiError(err)
  } finally {
    removeBusy.value = false
  }
}

function purgeMessage(service: AdminService | null) {
  if (!service) return ''
  return `${service.name} is deleted for good, along with everything it has recorded: its components, its status events, its poll runs and every alert pointing at it, including alerts that belong to other people. Nothing brings it back.`
}

async function confirmPurge() {
  if (!purging.value) return

  purgeBusy.value = true
  purgeError.value = ''

  try {
    const name = purging.value.name
    await $fetch(`/api/v1/services/${purging.value.slug}?force=true`, { method: 'DELETE' })
    purging.value = null
    actionError.value = ''
    actionNotice.value = `${name} and its history are gone for good.`
    await refresh()
  } catch (err) {
    purgeError.value = apiError(err)
  } finally {
    purgeBusy.value = false
  }
}

async function restore(service: AdminService) {
  actionError.value = ''
  try {
    await $fetch(`/api/v1/services/${service.slug}`, {
      method: 'PATCH',
      body: { active: true },
    })
    await refresh()
  } catch (err) {
    actionError.value = apiError(err)
  }
}

// A parked vendor is retried by the poller once an hour anyway; this is for
// when a profile has just been deployed and the wait is not wanted.
async function retryDetection(service: AdminService) {
  actionError.value = ''
  actionNotice.value = ''
  retrying.value = service.slug

  try {
    const result = await $fetch<{ profile: AdminProfile }>(
      `/api/v1/services/${service.slug}/detect`,
      { method: 'POST' },
    )
    actionNotice.value =
      `${service.name} is read by ${result.profile.name} now, so it is live and being polled.`
    await refresh()
  } catch (err) {
    actionError.value = apiError(err)
  } finally {
    retrying.value = ''
  }
}

async function onSaved() {
  editing.value = null
  await refresh()
}
</script>

<template>
  <div class="mx-auto max-w-6xl px-4 py-8">
    <header class="mb-6 flex flex-wrap items-start justify-between gap-4">
      <div>
        <h1 class="text-2xl font-bold tracking-tight">Services</h1>
        <p class="mt-1 text-sm opacity-70">
          <template v-if="canManageServices">
            Everything the poller knows about. Suggestions arrive as proposed and wait here.
          </template>
          <template v-else>
            Services you added. A new one is checked against every adapter, and it is published
            once one can read its page.
          </template>
        </p>
      </div>
      <NuxtLink to="/services/new" class="btn btn-primary btn-sm">Add service</NuxtLink>
    </header>

    <div
      v-if="created"
      role="status"
      class="alert mb-4"
      :class="createdParked ? 'alert-warning' : 'alert-success'"
    >
      <div>
        <p v-if="createdParked && canManageServices">
          {{ createdName }} was saved, but no adapter profile can read its page yet, so it is not
          polled. A new profile has to be written for its platform before it can be; until then the
          poller keeps trying it, and Retry on its row runs that check right away.
        </p>
        <p v-else-if="createdParked">
          {{ createdName }} was saved. It is being checked against every adapter, and it is
          published once one can read its page. If none can, staff look at it and you can still
          find it here meanwhile.
        </p>
        <p v-else>{{ createdName }} was added and is live.</p>
        <NuxtLink :to="`/services/${created}`" class="link text-sm">View detail</NuxtLink>
      </div>
    </div>

    <div class="mb-4 flex flex-wrap items-center gap-3">
      <input
        v-model="searchInput"
        type="search"
        class="input input-bordered w-full sm:max-w-sm"
        placeholder="Search by name, slug, or url"
        aria-label="Search services"
      />

      <select
        v-if="canManageServices"
        v-model="profile"
        class="select select-bordered w-full sm:w-56"
        aria-label="Filter services by profile"
      >
        <option value="">All profiles</option>
        <option v-for="name in profileOptions" :key="name" :value="name">{{ name }}</option>
      </select>

      <span v-if="pending" class="loading loading-spinner loading-sm opacity-60" />
      <span v-else-if="search" class="text-xs opacity-60">
        {{ filtered.length }} match{{ filtered.length === 1 ? '' : 'es' }}
      </span>
    </div>

    <StatusFilter
      v-model="stateFilter"
      :statuses="stateOptions"
      :clearable="clearable"
      label="Filter services by status"
      class="mb-4"
      @clear="clearList"
    />

    <div v-if="canManageServices" role="tablist" class="tabs tabs-box mb-4">
      <button
        v-for="s in stages"
        :key="s.value"
        role="tab"
        class="tab"
        :class="{ 'tab-active': stage === s.value }"
        @click="stage = s.value"
      >
        {{ s.label }}
      </button>
    </div>

    <p v-if="actionNotice" role="status" class="alert alert-success mb-4">{{ actionNotice }}</p>
    <p v-if="actionError" role="alert" class="alert alert-error mb-4">{{ actionError }}</p>

    <div v-if="error" role="alert" class="alert alert-error">
      Could not load the services. Try again in a moment.
    </div>

    <div v-else-if="pending && !services.length" class="flex justify-center py-16">
      <span class="loading loading-spinner" />
    </div>

    <template v-else>
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
              <th>Source</th>
              <th class="text-right">Actions</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="s in paged" :key="s.slug" class="align-top">
              <td>
                <NuxtLink :to="detailLink(s)" class="link link-hover font-medium">
                  {{ s.name }}
                </NuxtLink>
                <div class="font-mono text-xs opacity-50">/{{ s.slug }}</div>
                <div v-if="s.ownerId && canManageServices" class="mt-1">
                  <span class="badge badge-ghost badge-sm" :title="s.ownerEmail">
                    {{ s.status === 'proposed' ? `suggested by ${s.ownerName}` : `added by ${s.ownerName}` }}
                  </span>
                </div>
              </td>
              <td>
                <div class="flex flex-wrap items-center gap-1">
                  <span
                    class="badge badge-sm whitespace-nowrap"
                    :class="serviceStatusClass(s.status, s.active)"
                  >{{ serviceStatusLabel(s.status, s.active) }}</span>
                  <span
                    v-if="s.visibility === 'private'"
                    class="badge badge-ghost badge-sm"
                    title="Only you and the staff can see it"
                  >Private</span>
                </div>
              </td>
              <td>
                <span v-if="s.profile" class="font-mono text-xs">{{ s.profile }}</span>
                <span v-else class="text-xs opacity-50">none</span>
              </td>
              <td class="whitespace-nowrap text-sm opacity-60" :title="formatDate(s.updatedAt)">
                <LiveAgo :at="s.updatedAt" />
              </td>
              <td class="max-w-xs">
                <a
                  v-if="s.url"
                  :href="s.url"
                  target="_blank"
                  rel="noopener noreferrer"
                  class="link break-all text-xs"
                >
                  {{ s.url }}
                </a>
                <span v-else class="text-xs opacity-50">no url</span>
              </td>
              <td>
                <div class="flex justify-end">
                  <ServiceActions
                    :service="s"
                    :can-edit="canManageServices || ownsService(s)"
                    :can-manage="canManageServices"
                    :can-remove="canRemove(s)"
                    :can-purge="canPurge(s)"
                    :retrying="retrying === s.slug"
                    @edit="editing = s"
                    @publish="editing = { ...s, status: 'published' }"
                    @retry="retryDetection(s)"
                    @restore="restore(s)"
                    @remove="removing = s"
                    @purge="purging = s"
                  />
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <ul class="flex flex-col gap-3 md:hidden">
        <li
          v-for="s in paged"
          :key="s.slug"
          class="rounded-box border border-base-300 bg-base-100 p-4 shadow-sm"
        >
          <div class="flex items-start justify-between gap-3">
            <div class="min-w-0">
              <NuxtLink :to="detailLink(s)" class="link link-hover font-medium">
                {{ s.name }}
              </NuxtLink>
              <div class="font-mono text-xs opacity-50">/{{ s.slug }}</div>
            </div>
            <div class="flex shrink-0 flex-wrap items-center justify-end gap-1">
              <span
                class="badge badge-sm whitespace-nowrap"
                :class="serviceStatusClass(s.status, s.active)"
              >
                {{ serviceStatusLabel(s.status, s.active) }}
              </span>
              <span
                v-if="s.visibility === 'private'"
                class="badge badge-ghost badge-sm"
                title="Only you and the staff can see it"
              >Private</span>
            </div>
          </div>

          <div class="mt-2 flex flex-wrap items-center gap-2 text-xs opacity-70">
            <span class="font-mono">{{ s.profile || 'none' }}</span>
            <span aria-hidden="true">&middot;</span>
            <a
              v-if="s.url"
              :href="s.url"
              target="_blank"
              rel="noopener noreferrer"
              class="link break-all"
            >
              {{ s.url }}
            </a>
            <span v-else>no url</span>
          </div>

          <!-- Its own line: with the profile and the url above it, the time wraps
               around the middle of a phone screen and reads as part of the link. -->
          <div class="mt-1 text-xs opacity-70" :title="formatDate(s.updatedAt)">
            updated <LiveAgo :at="s.updatedAt" />
          </div>

          <div v-if="s.ownerId && canManageServices" class="mt-1">
            <span class="badge badge-ghost badge-sm" :title="s.ownerEmail">
              added by {{ s.ownerName }}
            </span>
          </div>

          <div class="mt-3">
            <ServiceActions
              :service="s"
              :can-edit="canManageServices || ownsService(s)"
              :can-manage="canManageServices"
              :can-remove="canRemove(s)"
              :can-purge="canPurge(s)"
              :retrying="retrying === s.slug"
              @edit="editing = s"
              @publish="editing = { ...s, status: 'published' }"
              @retry="retryDetection(s)"
              @restore="restore(s)"
              @remove="removing = s"
              @purge="purging = s"
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
        <template v-else-if="stage !== 'all' || stateFilter.length || profile">
          Nothing here for that filter.
        </template>
        <template v-else>Nothing here.</template>
      </p>
    </template>

    <ServiceEditor
      v-if="editing"
      :slug="editing.slug"
      :preset-status="editing.status"
      @close="editing = null"
      @saved="onSaved"
    />

    <ConfirmDialog
      v-if="removing"
      heading="Delete service"
      :message="removalMessage(removing)"
      confirm-label="Delete"
      :busy="removeBusy"
      :error="removeError"
      @cancel="removing = null"
      @confirm="confirmRemove"
    />

    <ConfirmDialog
      v-if="purging"
      heading="Force delete"
      :message="purgeMessage(purging)"
      confirm-label="Force delete"
      :busy="purgeBusy"
      :error="purgeError"
      @cancel="purging = null"
      @confirm="confirmPurge"
    />
  </div>
</template>
