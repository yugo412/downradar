<script setup lang="ts">
import type { AccountAlert, Stats, Vendor } from '~/types/api'

const { data, error } = await useFetch<{ vendors: Vendor[]; count: number }>('/api/v1/vendors')
const { data: stats } = await useFetch<Stats>('/api/v1/stats')

// The catalogue is rendered on the server and then cached for a minute, so a
// private service must never be part of it: the owner's own services are
// fetched and merged in the browser instead. A cached page therefore stays
// public, no matter who loaded it.
const { user, loaded } = useAuth()
const myVendors = ref<Vendor[]>([])

watch(
  [loaded, user],
  async () => {
    if (!loaded.value || !user.value) {
      myVendors.value = []
      return
    }
    try {
      const res = await $fetch<{ vendors: Vendor[] }>('/api/v1/services/mine')
      myVendors.value = res.vendors
    } catch {
      myVendors.value = []
    }
  },
  { immediate: true },
)

const catalogueSlugs = computed(() => new Set((data.value?.vendors ?? []).map((v) => v.slug)))

// The services this account added, private or published: a card for one of them
// opens the owner's own view of it, which is where the fetch log and the edit
// button are. One of them that is not in the catalogue is the private one, and
// its card says so.
const ownedSlugs = computed(() => new Set(myVendors.value.map((v) => v.slug)))

const privateSlugs = computed(
  () =>
    new Set(
      myVendors.value.filter((v) => !catalogueSlugs.value.has(v.slug)).map((v) => v.slug),
    ),
)

// How a card says the person reading it already watches that service: the bell
// the site marks an alert with, in the colour of what the alert is doing. A
// paused alert is still an alert, so the bell stays and says which it is.
const marks = ref<Map<string, { label: string; cls: string }>>(new Map())

const MARKS: Record<string, { label: string; cls: string }> = {
  active: { label: 'You get alerts for this service', cls: 'text-success' },
  paused: { label: 'Your alerts for this service are paused', cls: 'opacity-40' },
}

// Read from the account's own alerts once the page is up, because the catalogue
// itself is cached and served to everyone: nothing about one account can be in
// that HTML.
watch(
  [loaded, user],
  async () => {
    if (!loaded.value || !user.value) {
      marks.value = new Map()
      return
    }
    try {
      const res = await $fetch<{ alerts: AccountAlert[] }>('/api/v1/auth/alerts')
      const next = new Map<string, { label: string; cls: string }>()
      for (const alert of res.alerts) {
        // A row that was ended is not something they are watching any more.
        const mark = alert.state === 'unsubscribed' ? undefined : MARKS[alert.state]
        if (mark) next.set(alert.vendorSlug, mark)
      }
      marks.value = next
    } catch {
      // Nothing worth saying on the index: the cards go on without a bell.
      marks.value = new Map()
    }
  },
  { immediate: true },
)

const route = useRoute()
const router = useRouter()

// The chips and the order live in the query string, the way the services and the
// users screens keep theirs, so a view can be shared, bookmarked or come back with
// the back button. Whatever the URL carries when the page opens is what the list
// starts with.
function chosenStates(raw: unknown): string[] {
  return String(raw ?? '')
    .split(',')
    .map((state) => state.trim())
    .filter((state) => HEALTH_STATES.includes(state))
}

// ?state=degraded,partial. A state nobody knows is dropped, so a hand-edited URL
// narrows to what the page can show rather than to an empty list.
const statusFilter = ref(chosenStates(route.query.state))

// Worst first is what the page opens with; ?sort=name is the alphabetical order
// the toggle turns on.
const worstFirst = ref(String(route.query.sort ?? '') !== 'name')

// Narrowing to the services the person already watches. Unlike the chips beside
// it this one is not in the address: the bells are read from the account's own
// alerts in the browser, and a link carrying it would narrow to whoever opened
// it, or to nothing at all for a guest. It is dropped with a reload instead.
const subscribedOnly = ref(false)

// The same kind of chip for the services this account added, with the same
// reason for staying out of the address.
const mineOnly = ref(false)

// Signing out leaves nothing to narrow to, and a filter left on would empty the
// page with no chip in sight to turn it off.
watch(user, () => {
  subscribedOnly.value = false
  mineOnly.value = false
})

// The desktop chip row's reset drops the state chips; these two are not states,
// so they are dropped here. The mobile sheet clears all three itself.
function clearChips() {
  subscribedOnly.value = false
  mineOnly.value = false
}

// The inputs are bound to a local ref, not to the route query. The address is
// written asynchronously, so reading the value back from the URL while typing
// re-renders the field with a stale value and swallows keystrokes. The URL is
// updated separately and debounced, so a search is still shareable.
const queryInput = ref(String(route.query.q ?? ''))
const query = computed(() => queryInput.value)

// The URL follows the page, so the view on screen is the one that can be linked
// to. The default order is left out, so a plain view keeps a plain address, and
// nothing is written when the address already says what the page shows.
function syncUrl() {
  const params: Record<string, string> = {}
  const search = queryInput.value.trim()
  if (search) params.q = search
  if (statusFilter.value.length) params.state = statusFilter.value.join(',')
  if (!worstFirst.value) params.sort = 'name'

  const current = route.query
  const keys = Object.keys(params)
  const unchanged =
    keys.length === Object.keys(current).length &&
    keys.every((key) => String(current[key] ?? '') === params[key])
  if (!unchanged) router.replace({ query: params })
}

let urlSyncTimer: ReturnType<typeof setTimeout> | undefined
watch(queryInput, () => {
  clearTimeout(urlSyncTimer)
  urlSyncTimer = setTimeout(syncUrl, 250)
})

onBeforeUnmount(() => clearTimeout(urlSyncTimer))

watch([statusFilter, worstFirst], syncUrl)

// Follow the URL when it changes from somewhere else, such as the back button or
// a shared link, without fighting the clicks and the typing above. Each one is
// compared before it is written, or the page would answer its own replace with
// another one.
watch(
  () => route.query,
  (value) => {
    const search = String(value.q ?? '')
    if (search !== queryInput.value.trim()) queryInput.value = search

    const states = chosenStates(value.state)
    if (states.join(',') !== statusFilter.value.join(',')) statusFilter.value = states

    const worst = String(value.sort ?? '') !== 'name'
    if (worst !== worstFirst.value) worstFirst.value = worst
  },
)

const vendors = computed<Vendor[]>(() => {
  const catalogue = data.value?.vendors ?? []
  // A service the viewer published is in the catalogue and in their own list.
  // The catalogue copy is the one that keeps it from being drawn twice.
  return [...catalogue, ...myVendors.value.filter((v) => !catalogueSlugs.value.has(v.slug))]
})

// The heading names what is actually wrong instead of asking a question nobody
// answers, rotating through up to five of them, worst first. It reads the public
// catalogue rather than the list below it: a private service belongs to one
// person and this page is cached and served to everyone.
//
// Only the six health states count. A vendor whose page could not be matched is
// not down, it is unknown to us, and its name is not the sort of thing to put in
// a heading.
const down = computed(() =>
  (data.value?.vendors ?? [])
    .filter((v) => HEALTH_STATES.includes(v.status) && !isHealthy(v.status))
    .sort((a, b) => severity(b.status) - severity(a.status) || a.name.localeCompare(b.name))
    .slice(0, 5),
)

// daisyUI's text rotate is as wide as its longest line, so a short name leaves a
// gap between it and the rest of the sentence. The box is narrowed to whichever
// line is on screen, read from the box itself rather than counted off a clock, so
// the two cannot drift apart. It is watched frame by frame, which is what keeps
// it in step with the animation and costs nothing while the tab is in the
// background: this is the one place where a frame is the right unit, unlike the
// shared clock behind "N ago", which ticks once a second for a hundred values.
const rotating = useTemplateRef<HTMLElement>('rotating')

onMounted(() => {
  const box = rotating.value
  const track = box?.firstElementChild as HTMLElement | null
  if (!box || !track || track.children.length < 2) return

  const lines = [...track.children] as HTMLElement[]
  let frame = 0

  const narrow = () => {
    const boxRect = box.getBoundingClientRect()
    const shown = lines.find((line) => {
      const rect = line.getBoundingClientRect()
      return rect.top >= boxRect.top - 1 && rect.bottom <= boxRect.bottom + 1
    })

    if (shown) {
      const width = Math.round(shown.getBoundingClientRect().width)
      if (box.offsetWidth !== width) box.style.width = `${width}px`
    }
    frame = requestAnimationFrame(narrow)
  }

  narrow()
  onBeforeUnmount(() => cancelAnimationFrame(frame))
})

const matched = computed(() => {
  const q = query.value.trim().toLowerCase()
  if (!q) return vendors.value
  return vendors.value.filter(
    (v) =>
      v.name.toLowerCase().includes(q) ||
      v.slug.includes(q) ||
      (v.category ?? '').toLowerCase().includes(q),
  )
})

// The badge counts are counted after the search, so the number next to a state
// always agrees with the cards underneath it.
const statusOptions = computed(() =>
  statusCounts(matched.value, (v) => v.status, statusFilter.value),
)

// The Subscribed chip carries the same kind of number: the services in this list
// that the person is watching, which is what picking it leaves on screen.
const subscribedCount = computed(
  () => matched.value.filter((v) => marks.value.has(v.slug)).length,
)

const mineCount = computed(
  () => matched.value.filter((v) => ownedSlugs.value.has(v.slug)).length,
)

const visible = computed(() => {
  let base = matched.value
  if (statusFilter.value.length) {
    base = base.filter((v) => statusFilter.value.includes(v.status))
  }
  if (subscribedOnly.value) {
    base = base.filter((v) => marks.value.has(v.slug))
  }
  if (mineOnly.value) {
    base = base.filter((v) => ownedSlugs.value.has(v.slug))
  }

  const sorted = [...base].sort((a, b) =>
    worstFirst.value
      ? severity(b.status) - severity(a.status) || a.name.localeCompare(b.name)
      : a.name.localeCompare(b.name),
  )

  // The mark is carried on the card itself, so a card asks for it once rather
  // than looking the same service up again in the template.
  return sorted.map((vendor) => ({ ...vendor, mark: marks.value.get(vendor.slug) ?? null }))
})

// A card saying "no match" under a filter that is out of sight is a puzzle, so
// the pick that emptied the list is named, along with what to do about it.
const emptyTitle = computed(() =>
  subscribedOnly.value || mineOnly.value
    ? 'No service here matches the filters you picked'
    : 'No service is in the states you picked',
)

const emptyHint = computed(() => {
  const picked = [subscribedOnly.value ? 'Subscribed' : '', mineOnly.value ? 'My services' : '']
    .filter(Boolean)
    .join(' or ')

  if (picked && statusFilter.value.length) {
    return `Clear the status filter or ${picked} to see the rest of the catalogue.`
  }
  if (subscribedOnly.value) {
    return 'You are not watching any of these yet. Open one and use its bell to follow it.'
  }
  if (mineOnly.value) {
    return 'You have not added a service yet. Add one and it appears here once its page can be read.'
  }
  return 'Clear the status filter to see the rest of the catalogue.'
})

type SuggestState = 'idle' | 'sending' | 'sent' | 'exists' | 'error'

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
    await $fetch<{ status: string; slug: string }>('/api/v1/suggest', {
      method: 'POST',
      body: { name: suggestName.value, url: suggestUrl.value },
    })
    suggestState.value = 'sent'
  } catch (err: unknown) {
    const data = (err as { data?: { error?: string; vendorSlug?: string } })?.data

    if (data?.vendorSlug) {
      suggestState.value = 'exists'
      suggestMessage.value = data.error ?? 'That service has already been suggested.'
      suggestLink.value = `/${data.vendorSlug}`
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
      <h1 class="text-3xl font-bold tracking-tight">
        <template v-if="down.length">
          Is
          <span ref="rotating" class="text-rotate">
            <span>
              <span v-for="v in down" :key="v.slug" :class="statusMeta(v.status).ink">{{ v.name }}</span>
            </span>
          </span>
          down right now?
        </template>
        <template v-else>Is it down right now?</template>
      </h1>
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

    <section class="mb-6 flex flex-col gap-3 md:flex-row md:items-center">
      <div class="relative w-full md:flex-1">
        <svg
          xmlns="http://www.w3.org/2000/svg"
          class="pointer-events-none absolute left-3 top-1/2 z-10 h-4 w-4 -translate-y-1/2 opacity-50"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="2"
          stroke-linecap="round"
          stroke-linejoin="round"
          aria-hidden="true"
        >
          <circle cx="11" cy="11" r="7" />
          <path d="m20 20-3.5-3.5" />
        </svg>
        <input
          v-model="queryInput"
          type="search"
          placeholder="Search a service, e.g. strava"
          aria-label="Search services"
          class="input input-bordered w-full pl-9"
        />
      </div>

      <div role="group" aria-label="Sort services" class="join w-full md:w-auto">
        <button
          type="button"
          class="btn join-item flex-1 md:flex-none"
          :class="worstFirst ? 'btn-primary' : 'bg-base-100'"
          :aria-pressed="worstFirst"
          @click="worstFirst = true"
        >
          Problems first
        </button>
        <button
          type="button"
          class="btn join-item flex-1 md:flex-none"
          :class="worstFirst ? 'bg-base-100' : 'btn-primary'"
          :aria-pressed="!worstFirst"
          @click="worstFirst = false"
        >
          Name A–Z
        </button>
      </div>
    </section>

    <!-- A narrow screen gets one trigger and a sheet, because the states and
         the account's own filters do not fit a row there. From md up the chips
         stay in view, which is faster when there is room for them. The account's
         own filters only appear for somebody signed in: a guest typed an
         address, not an account, so there is nothing of theirs to narrow to.
         They wait for the account's own rows to be read in the browser, and an
         option with nothing under it is not shown, the same way a state no card
         is in is not. -->
    <div class="mb-6">
      <CatalogueFilter
        v-model="statusFilter"
        v-model:mine="mineOnly"
        v-model:subscribed="subscribedOnly"
        :statuses="statusOptions"
        :mine-count="mineCount"
        :subscribed-count="subscribedCount"
        :show-mine="!!user && (!!mineCount || mineOnly)"
        :show-subscribed="!!user && (!!subscribedCount || subscribedOnly)"
        :result-count="visible.length"
        label="Filter services"
        class="md:hidden"
      />

      <StatusFilter
        v-model="statusFilter"
        :statuses="statusOptions"
        label="Filter services"
        class="hidden md:flex"
        @clear="clearChips"
      >
        <FilterChip
          v-if="user && (mineCount || mineOnly)"
          v-model="mineOnly"
          label="My services"
          :count="mineCount"
        />
        <FilterChip
          v-if="user && (subscribedCount || subscribedOnly)"
          v-model="subscribedOnly"
          label="Subscribed"
          :count="subscribedCount"
        />
      </StatusFilter>
    </div>

    <div v-if="error" role="alert" class="alert alert-error">
      <span>Could not load the status list. The API may still be warming up.</span>
    </div>

    <div v-else-if="!vendors.length" class="flex items-center gap-3 py-16 justify-center opacity-70">
      <span class="loading loading-spinner" />
      <span>Waiting for the first poll cycle…</span>
    </div>

    <template v-else>
      <div v-if="!matched.length" class="card bg-base-100 border border-base-300">
        <div class="card-body gap-3">
          <div>
            <h2 class="text-lg font-semibold">No match for “{{ query }}”</h2>
            <p class="mt-1 text-sm opacity-70">
              We do not monitor that service yet. Suggest it and we will take a look.
            </p>
          </div>

          <div v-if="suggestState === 'sent'" role="status" class="alert alert-success">
            <div>
              <p class="font-medium">Thanks, your suggestion was recorded.</p>
              <p class="text-sm opacity-80">
                A moderator will review it before it starts being monitored.
              </p>
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
                Goes to a review queue, nothing is monitored until it is approved.
              </span>
            </div>

            <p v-if="suggestState === 'error'" class="text-sm text-error">{{ suggestMessage }}</p>
          </form>
        </div>
      </div>

      <div v-else-if="!visible.length" class="card bg-base-100 border border-base-300">
        <div class="card-body">
          <h2 class="text-lg font-semibold">{{ emptyTitle }}</h2>
          <p class="mt-1 text-sm opacity-70">
            {{ emptyHint }}
          </p>
        </div>
      </div>

      <section v-else class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        <NuxtLink
          v-for="v in visible"
          :key="v.slug"
          :to="ownedSlugs.has(v.slug) ? `/services/${v.slug}` : `/${v.slug}`"
          class="card bg-base-100 border border-base-300 hover:border-primary transition-colors"
        >
          <div class="card-body p-4">
            <div class="flex items-start gap-3">
              <VendorLogo :src="v.logoUrl" :name="v.name" :size="40" />
              <div class="min-w-0 flex-1">
                <div class="flex items-center justify-between gap-2">
                  <h2 class="text-lg font-semibold leading-snug">{{ v.name }}</h2>
                  <span class="flex shrink-0 items-center gap-1.5">
                    <span
                      v-if="v.mark"
                      role="img"
                      :aria-label="v.mark.label"
                      :title="v.mark.label"
                    >
                      <BellIcon class="h-4 w-4" :class="v.mark.cls" />
                    </span>
                    <StatusBadge :status="v.status" size="sm" />
                  </span>
                </div>
                <p class="flex items-center gap-1.5 text-xs opacity-60">
                  <span>{{ v.category || 'service' }}</span>
                  <span
                    v-if="privateSlugs.has(v.slug)"
                    class="badge badge-ghost badge-sm"
                    title="Private: only you can see this one."
                  >
                    private
                  </span>
                </p>
              </div>
            </div>

            <div class="mt-3 flex flex-wrap items-center justify-between gap-x-2 gap-y-1 text-xs opacity-60">
              <span>{{ v.components }} components</span>
              <span>
                <LiveAgo :at="v.changedAt" /> in this state &middot; checked <LiveAgo :at="v.lastPolledAt" />
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
