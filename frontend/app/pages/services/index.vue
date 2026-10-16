<script setup lang="ts">
import type { AdminProfile, AdminService, ServiceStatus } from '~/types/api'

definePageMeta({ middleware: 'staff', ssr: false })

useSeoMeta({ title: 'Services', robots: 'noindex, nofollow' })

type Filter = 'all' | ServiceStatus | 'removed'

const filters: { value: Filter; label: string }[] = [
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

const requested = String(route.query.filter ?? 'all') as Filter
const filter = ref<Filter>(filters.some((f) => f.value === requested) ? requested : 'all')

// The input keeps its own value and the request is debounced, so typing does
// not fire one query per keystroke. The URL follows along, so a search is
// shareable and the back button works.
const searchInput = ref(String(route.query.q ?? ''))
const search = ref(searchInput.value.trim())

const { data, pending, error, refresh } = useFetch<{ services: AdminService[]; count: number }>(
  () => `/api/v1/services?filter=${filter.value}&q=${encodeURIComponent(search.value)}`,
)

const services = computed(() => data.value?.services ?? [])
const created = computed(() => String(route.query.created ?? ''))

// The query carries the slug of a service that was just added, so the notice can
// say what became of it. A vendor that no profile could read is saved as
// no_match and waits for an adapter, which is worth explaining there.
const createdService = computed(() => services.value.find((s) => s.slug === created.value) ?? null)
const createdName = computed(() => createdService.value?.name ?? created.value)
const createdParked = computed(() => createdService.value?.status === 'no_match')

const editing = ref<AdminService | null>(null)
const removing = ref<AdminService | null>(null)
const removeBusy = ref(false)
const removeError = ref('')
const actionError = ref('')
const actionNotice = ref('')
const retrying = ref('')

function syncUrl() {
  const query: Record<string, string> = {}
  if (filter.value !== 'all') query.filter = filter.value
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

// Where the name links to. A private service, and a vendor that is not published
// yet, has no page on the public site, so both get the signed in view under
// /services, which is also where their fetch log is.
function detailLink(service: AdminService) {
  if (!service.active) return ''
  if (service.ownerId || service.status !== 'published') return `/services/${service.slug}`
  return `/${service.slug}`
}

function removalMessage(service: AdminService | null) {
  if (!service) return ''
  if (service.status === 'proposed') {
    return `${service.name} was only a suggestion, so it is deleted for good. Nothing else is affected.`
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
          <template v-else>Services that only you can see.</template>
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
        <p v-if="createdParked">
          {{ createdName }} was saved, but no adapter profile can read its page yet, so it is not
          polled. A new profile has to be written for its platform before it can be; until then the
          poller keeps trying it, and Retry on its row runs that check right away.
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
      <span v-if="pending" class="loading loading-spinner loading-sm opacity-60" />
      <span v-else-if="search" class="text-xs opacity-60">
        {{ services.length }} match{{ services.length === 1 ? '' : 'es' }}
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
              <th>Service</th>
              <th>Status</th>
              <th>Profile</th>
              <th>Source</th>
              <th class="text-right">Actions</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="s in services" :key="s.slug" class="align-top">
              <td>
                <NuxtLink
                  v-if="detailLink(s)"
                  :to="detailLink(s)"
                  class="link link-hover font-medium"
                >
                  {{ s.name }}
                </NuxtLink>
                <div
                  v-else
                  class="font-medium"
                  title="A service only gets a page once it is published."
                >
                  {{ s.name }}
                </div>
                <div class="font-mono text-xs opacity-50">/{{ s.slug }}</div>
                <div v-if="s.ownerId" class="mt-1">
                  <span class="badge badge-ghost badge-sm" :title="s.ownerEmail">
                    {{ s.status === 'proposed' ? `suggested by ${s.ownerName}` : `private · ${s.ownerName}` }}
                  </span>
                </div>
              </td>
              <td>
                <span
                  class="badge badge-sm whitespace-nowrap"
                  :class="serviceStatusClass(s.status, s.active)"
                >{{ serviceStatusLabel(s.status, s.active) }}</span>
              </td>
              <td>
                <span v-if="s.profile" class="font-mono text-xs">{{ s.profile }}</span>
                <span v-else class="text-xs opacity-50">none</span>
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
                    :can-remove="canRemove(s)"
                    :retrying="retrying === s.slug"
                    @edit="editing = s"
                    @publish="editing = { ...s, status: 'published' }"
                    @retry="retryDetection(s)"
                    @restore="restore(s)"
                    @remove="removing = s"
                  />
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <ul class="flex flex-col gap-3 md:hidden">
        <li
          v-for="s in services"
          :key="s.slug"
          class="rounded-box border border-base-300 bg-base-100 p-4 shadow-sm"
        >
          <div class="flex items-start justify-between gap-3">
            <div class="min-w-0">
              <NuxtLink
                v-if="detailLink(s)"
                :to="detailLink(s)"
                class="link link-hover font-medium"
              >
                {{ s.name }}
              </NuxtLink>
              <div
                v-else
                class="font-medium"
                title="A service only gets a page once it is published."
              >
                {{ s.name }}
              </div>
              <div class="font-mono text-xs opacity-50">/{{ s.slug }}</div>
            </div>
            <span
              class="badge badge-sm shrink-0 whitespace-nowrap"
              :class="serviceStatusClass(s.status, s.active)"
            >
              {{ serviceStatusLabel(s.status, s.active) }}
            </span>
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

          <div v-if="s.ownerId" class="mt-1">
            <span class="badge badge-ghost badge-sm" :title="s.ownerEmail">
              private &middot; {{ s.ownerName }}
            </span>
          </div>

          <div class="mt-3">
            <ServiceActions
              :service="s"
              :can-remove="canRemove(s)"
              :retrying="retrying === s.slug"
              @edit="editing = s"
              @publish="editing = { ...s, status: 'published' }"
              @retry="retryDetection(s)"
              @restore="restore(s)"
              @remove="removing = s"
            />
          </div>
        </li>
      </ul>

      <p
        v-if="!services.length"
        class="rounded-box border border-base-300 bg-base-100 p-6 text-center text-sm opacity-70 shadow-sm"
      >
        <template v-if="search">Nothing matches &ldquo;{{ search }}&rdquo;.</template>
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
  </div>
</template>
