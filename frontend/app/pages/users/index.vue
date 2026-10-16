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

function syncUrl() {
  const query: Record<string, string> = {}
  if (filter.value !== 'all') query.role = filter.value
  if (search.value) query.q = search.value
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

watch(filter, syncUrl)

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

// Deleting takes the services they own with them, because owner_id cascades, so
// the warning has to say so.
function removalMessage(target: AdminUser | null) {
  if (!target) return ''

  const base = `${target.name} (${target.email}) is removed for good, and any session they have ends.`
  if (target.services === 0) return base

  const many = target.services > 1
  return `${base} The ${target.services} service${many ? 's' : ''} they own ${many ? 'are' : 'is'} deleted with them, history included.`
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

    <div role="tablist" class="tabs tabs-box mb-4">
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
              <th>Account</th>
              <th>Role</th>
              <th>Services</th>
              <th>Status</th>
              <th class="text-right">Actions</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="u in users" :key="u.id" class="align-top">
              <td>
                <div class="font-medium">
                  {{ u.name }}
                  <span v-if="isMe(u)" class="badge badge-ghost badge-sm ml-1">you</span>
                </div>
                <div class="text-xs break-all opacity-60">{{ u.email }}</div>
              </td>
              <td>
                <span class="badge badge-sm whitespace-nowrap" :class="roleClass(u.role)">
                  {{ u.role }}
                </span>
              </td>
              <td class="text-sm opacity-70">{{ u.services }}</td>
              <td>
                <span v-if="u.disabledAt" class="badge badge-error badge-sm">disabled</span>
                <span v-else class="badge badge-success badge-sm">active</span>
                <span
                  v-if="!u.verifiedAt"
                  class="badge badge-warning badge-sm ml-1 whitespace-nowrap"
                  title="This address has not been confirmed"
                >
                  unconfirmed
                </span>
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
          v-for="u in users"
          :key="u.id"
          class="rounded-box border border-base-300 bg-base-100 p-4 shadow-sm"
        >
          <div class="flex items-start justify-between gap-3">
            <div class="min-w-0">
              <div class="font-medium">
                {{ u.name }}
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
            <span v-if="!u.verifiedAt" class="badge badge-warning badge-sm whitespace-nowrap">
              unconfirmed
            </span>
            <span class="text-xs opacity-60">
              {{ u.services }} service{{ u.services === 1 ? '' : 's' }}
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

      <p
        v-if="!users.length"
        class="rounded-box border border-base-300 bg-base-100 p-6 text-center text-sm opacity-70 shadow-sm"
      >
        <template v-if="search">Nothing matches &ldquo;{{ search }}&rdquo;.</template>
        <template v-else>No accounts yet.</template>
      </p>
    </template>

    <p class="mt-4 text-xs opacity-60">
      Disabling keeps everything and can be undone. Deleting cannot: it also removes the private
      services that account owns. Neither is offered for your own account.
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
