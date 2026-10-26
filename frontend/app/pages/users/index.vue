<script setup lang="ts">
import type { AdminUser, Role } from '~/types/api'

definePageMeta({ middleware: 'admin', ssr: false })

useSeoMeta({ title: 'Users', robots: 'noindex, nofollow' })

const { user: me } = useAuth()

type Filter = 'all' | Role

const filters: { value: Filter; label: string }[] = [
  { value: 'all', label: 'All' },
  { value: 'admin', label: 'Admin' },
  { value: 'moderator', label: 'Moderator' },
  { value: 'subscriber', label: 'Subscriber' },
]

const route = useRoute()
const router = useRouter()

// Every filter and the sort live in the query string, so a view can be shared,
// bookmarked, or come back with the back button.
const requested = String(route.query.role ?? 'all') as Filter
const filter = ref<Filter>(filters.some((f) => f.value === requested) ? requested : 'all')

// The input keeps its own value and the request is debounced, so typing does not
// fire one query per keystroke. The URL follows along, so a search is shareable.
const searchInput = ref(String(route.query.q ?? ''))
const search = ref(searchInput.value.trim())

const { data, pending, error, refresh } = useFetch<{ users: AdminUser[]; count: number }>(
  () => `/api/v1/users?role=${filter.value === 'all' ? '' : filter.value}&q=${encodeURIComponent(search.value)}`,
)

const users = computed(() => data.value?.users ?? [])

// Sorting happens here rather than in the query, because every row is already in
// the page and a click should not wait for a round trip. A header sorts by itself
// first, and clicking the same one again turns it around. Until a header is
// clicked the list keeps the order the API sent, which puts disabled accounts
// last.
type SortKey = 'name' | 'role' | 'alerts' | 'status'

const sortColumns: { key: SortKey; label: string }[] = [
  { key: 'name', label: 'Account' },
  { key: 'role', label: 'Role' },
  { key: 'alerts', label: 'Alerts' },
  { key: 'status', label: 'Status' },
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

function sortField(target: AdminUser, key: SortKey): string | number {
  switch (key) {
    case 'name':
      return target.name
    case 'role':
      return target.role
    case 'alerts':
      return target.alerts
    case 'status':
      return target.disabledAt ? 1 : 0
  }
}

const sorted = computed(() => {
  const key = sortKey.value
  if (!key) return users.value

  const direction = sortAsc.value ? 1 : -1
  return [...users.value].sort((a, b) => {
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

// A page can lose its rows from under it, when the search narrows or an account
// is deleted. Reading it clamped, rather than leaving it pointing past the end,
// keeps the list on screen.
const currentPage = computed(() => Math.min(page.value, pageCount.value))

const paged = computed(() =>
  sorted.value.slice((currentPage.value - 1) * PAGE_SIZE, currentPage.value * PAGE_SIZE),
)

const firstRow = computed(() => (sorted.value.length ? (currentPage.value - 1) * PAGE_SIZE + 1 : 0))
const lastRow = computed(() => Math.min(currentPage.value * PAGE_SIZE, sorted.value.length))

// A filter, a search or a sort is a new list, so it starts at the top.
watch([filter, search, sortKey, sortAsc], () => {
  page.value = 1
})

// What a header says about the sort, to a reader and to a screen reader alike.
function sortState(key: SortKey) {
  if (sortKey.value !== key) return 'none'
  return sortAsc.value ? 'ascending' : 'descending'
}

// Everything Clear would put back: the role, or an order.
const clearable = computed(() => filter.value !== 'all' || sortKey.value !== '')

// Clear puts the list back to what the page opens with. The search box is left
// alone, because a term somebody typed is their own to remove.
function clearList() {
  filter.value = 'all'
  sortKey.value = ''
  sortAsc.value = true
}

// The URL follows the page, so the view on screen is the one that can be linked
// to. Only the search term is followed the other way, because a shared link is
// how a search usually arrives.
function syncUrl() {
  const query: Record<string, string> = {}
  if (filter.value !== 'all') query.role = filter.value
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

watch([filter, sortKey, sortAsc], syncUrl)

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

const editing = ref<AdminUser | null>(null)
const removing = ref<AdminUser | null>(null)
const removeBusy = ref(false)
const removeError = ref('')
const actionError = ref('')

function isMe(target: AdminUser) {
  return target.id === me.value?.id
}

function roleClass(role: Role) {
  if (role === 'admin') return 'badge-primary'
  if (role === 'moderator') return 'badge-info'
  return 'badge-ghost'
}

async function setDisabled(target: AdminUser, disabled: boolean) {
  actionError.value = ''

  try {
    await $fetch(`/api/v1/users/${target.id}`, { method: 'PATCH', body: { disabled } })
    await refresh()
  } catch (err) {
    actionError.value = apiError(err)
  }
}

// Deleting leaves the services they added behind, with owner_id cleared, so the
// warning has to say that rather than promising a tidy removal.
function removalMessage(target: AdminUser | null) {
  if (!target) return ''

  const base = `${target.name} (${target.email}) is removed for good, and any session they have ends.`
  if (target.services === 0) return base

  const many = target.services > 1
  return `${base} The ${target.services} service${many ? 's' : ''} they added ${many ? 'stay' : 'stays'} in the catalogue, with the owner cleared.`
}

async function confirmRemove() {
  if (!removing.value) return

  removeBusy.value = true
  removeError.value = ''

  try {
    await $fetch(`/api/v1/users/${removing.value.id}`, { method: 'DELETE' })
    removing.value = null
    await refresh()
  } catch (err) {
    removeError.value = apiError(err)
  } finally {
    removeBusy.value = false
  }
}

async function onSaved() {
  editing.value = null
  await refresh()
}
</script>

<template>
  <div class="mx-auto max-w-6xl px-4 py-8">
    <header class="mb-6">
      <h1 class="text-2xl font-bold tracking-tight">Users</h1>
      <p class="mt-1 text-sm opacity-70">
        Accounts are created from the shell with <code class="font-mono">downradar-user</code>.
        Here you change what they are, or switch them off.
      </p>
    </header>

    <p v-if="actionError" role="alert" class="alert alert-error mb-4">{{ actionError }}</p>

    <div class="mb-4 flex flex-wrap items-center gap-3">
      <input
        v-model="searchInput"
        type="search"
        class="input input-bordered w-full sm:max-w-sm"
        placeholder="Search by name or email"
        aria-label="Search accounts"
      />
      <span v-if="pending" class="loading loading-spinner loading-sm opacity-60" />
      <span v-else-if="search" class="text-xs opacity-60">
        {{ users.length }} match{{ users.length === 1 ? '' : 'es' }}
      </span>
    </div>

    <div class="mb-4 flex flex-wrap items-center gap-3">
      <div role="tablist" class="tabs tabs-box">
        <button
          v-for="f in filters"
          :key="f.value"
          role="tab"
          class="tab"
          :class="{ 'tab-active': filter === f.value }"
          @click="filter = f.value"
        >
          {{ f.label }}
        </button>
      </div>

      <!-- Clear also drops the sort, which no visible control shows the way back
           from; the search box is its own to empty and is left alone. -->
      <button v-if="clearable" type="button" class="link text-xs" @click="clearList">Clear</button>
    </div>

    <div v-if="error" role="alert" class="alert alert-error">
      Could not load the accounts. Try again in a moment.
    </div>

    <div v-else-if="pending && !users.length" class="flex justify-center py-16">
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
              <th class="text-right">Actions</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="u in paged" :key="u.id" class="align-top">
              <td>
                <div class="font-medium">
                  {{ u.name }}
                  <span
                    v-if="u.verifiedAt"
                    class="ml-1 text-success"
                    role="img"
                    aria-label="Email confirmed"
                    title="Email confirmed"
                  >✓</span>
                  <span v-if="isMe(u)" class="badge badge-ghost badge-sm ml-1">you</span>
                </div>
                <div class="text-xs break-all opacity-60">{{ u.email }}</div>
              </td>
              <td>
                <span class="badge badge-sm whitespace-nowrap" :class="roleClass(u.role)">
                  {{ u.role }}
                </span>
              </td>
              <td class="text-sm opacity-70">{{ u.alerts }}</td>
              <td>
                <span v-if="u.disabledAt" class="badge badge-error badge-sm">disabled</span>
                <span v-else class="badge badge-success badge-sm">active</span>
              </td>
              <td>
                <div class="flex justify-end">
                  <UserActions
                    :user="u"
                    :me="isMe(u)"
                    @edit="editing = u"
                    @toggle="setDisabled(u, !u.disabledAt)"
                    @remove="removing = u"
                  />
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <ul class="flex flex-col gap-3 md:hidden">
        <li
          v-for="u in paged"
          :key="u.id"
          class="rounded-box border border-base-300 bg-base-100 p-4 shadow-sm"
        >
          <div class="flex items-start justify-between gap-3">
            <div class="min-w-0">
              <div class="font-medium">
                {{ u.name }}
                <span
                  v-if="u.verifiedAt"
                  class="ml-1 text-success"
                  role="img"
                  aria-label="Email confirmed"
                  title="Email confirmed"
                >✓</span>
                <span v-if="isMe(u)" class="badge badge-ghost badge-sm ml-1">you</span>
              </div>
              <div class="text-xs break-all opacity-60">{{ u.email }}</div>
            </div>
            <span class="badge badge-sm shrink-0 whitespace-nowrap" :class="roleClass(u.role)">
              {{ u.role }}
            </span>
          </div>

          <div class="mt-2 flex flex-wrap items-center gap-2">
            <span v-if="u.disabledAt" class="badge badge-error badge-sm">disabled</span>
            <span v-else class="badge badge-success badge-sm">active</span>
            <span class="text-xs opacity-60">
              {{ u.alerts }} alert{{ u.alerts === 1 ? '' : 's' }}
            </span>
          </div>

          <div class="mt-3">
            <UserActions
              :user="u"
              :me="isMe(u)"
              @edit="editing = u"
              @toggle="setDisabled(u, !u.disabledAt)"
              @remove="removing = u"
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
        v-if="!users.length"
        class="rounded-box border border-base-300 bg-base-100 p-6 text-center text-sm opacity-70 shadow-sm"
      >
        <template v-if="search">Nothing matches &ldquo;{{ search }}&rdquo;.</template>
        <template v-else-if="filter !== 'all'">Nothing here for that filter.</template>
        <template v-else>No accounts yet.</template>
      </p>
    </template>

    <p class="mt-4 text-xs opacity-60">
      Disabling keeps everything and can be undone. Deleting cannot: it removes the account and
      its alerts, while the services they added stay in the catalogue without an owner. Neither
      is offered for your own account here.
    </p>

    <UserEditor v-if="editing" :user="editing" @close="editing = null" @saved="onSaved" />

    <ConfirmDialog
      v-if="removing"
      heading="Delete account"
      :message="removalMessage(removing)"
      confirm-label="Delete"
      :busy="removeBusy"
      :error="removeError"
      @cancel="removing = null"
      @confirm="confirmRemove"
    />
  </div>
</template>
