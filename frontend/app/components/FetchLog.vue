<script setup lang="ts">
import type { PollRun } from '~/types/api'

const props = defineProps<{ slug: string; ownerId?: number; flush?: boolean }>()

const { user, canManageServices } = useAuth()

// Staff and the owner of the row, which the API asks for again on its own.
// Stricter than the vendor itself: a public vendor is visible to every signed in
// visitor, and the fetch log is not.
const allowed = computed(
  () => canManageServices.value || (props.ownerId != null && props.ownerId === user.value?.id),
)

const {
  data,
  error,
  pending,
  refresh,
} = useFetch<{ polls: PollRun[]; nextPollAt?: string | null }>(
  () => `/api/v1/services/${props.slug}/polls`,
  { immediate: false },
)

const polls = computed(() => data.value?.polls ?? [])

// When a refused page will be tried again is staff business: the API leaves the
// value out for the owner. It counts down with the same clock the "ago" values
// use, so it moves while the page is open.
const now = useNow()
const { format: formatLocal } = useLocalTime()
const nextPollAt = computed(() => data.value?.nextPollAt ?? null)
const wait = computed(() => {
  if (!canManageServices.value || !nextPollAt.value) return ''
  if (new Date(nextPollAt.value).getTime() <= now.value) return ''
  return timeUntil(nextPollAt.value, now.value)
})

// Who is asking is not known when this mounts: staff learn it once the session
// plugin has run, the owner once the page has the service. The slug is watched
// as well, so moving to another vendor does not leave the old log up.
watch(
  [allowed, () => props.slug],
  () => {
    if (allowed.value) refresh()
  },
  { immediate: true },
)
</script>

<template>
  <section v-if="allowed" :class="flush ? '' : 'mt-10'">
    <h2 class="text-lg font-semibold" :class="wait ? 'mb-1' : 'mb-3'">Fetch log</h2>
    <p v-if="wait" class="mb-3 text-sm opacity-70" :title="formatLocal(nextPollAt)">
      The page refused the last poll. Next attempt {{ wait }}.
    </p>
    <div class="overflow-x-auto bg-base-100 border border-base-300 rounded-box shadow-sm">
      <table class="table table-sm">
        <thead>
          <tr>
            <th>Fetched</th>
            <th>Result</th>
            <th>HTTP</th>
            <th>Components</th>
            <th class="text-right">Duration</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="run in polls" :key="run.id" class="align-top">
            <td class="whitespace-nowrap">
              <LiveAgo :at="run.fetchedAt" />
              <div class="text-xs opacity-50"><LocalTime :at="run.fetchedAt" /></div>
            </td>
            <td>
              <span class="badge badge-sm" :class="run.ok ? 'badge-success' : 'badge-error'">
                {{ run.ok ? 'ok' : 'failed' }}
              </span>
              <div v-if="run.error" class="mt-1 text-xs text-error break-all">
                {{ run.error }}
              </div>
            </td>
            <td class="text-sm opacity-70">{{ run.httpStatus ?? '—' }}</td>
            <td class="text-sm opacity-70">
              <template v-if="run.ok">{{ run.componentsFound }}</template>
              <template v-else>—</template>
              <span
                v-if="run.unknownStatuses?.length"
                class="badge badge-warning badge-sm ml-2"
                :title="run.unknownStatuses.join(', ')"
              >
                {{ run.unknownStatuses.length }} unknown
              </span>
            </td>
            <td class="text-right text-sm opacity-70">
              {{ run.durationMs != null ? `${run.durationMs} ms` : '—' }}
            </td>
          </tr>
          <tr v-if="error">
            <td colspan="5" class="text-center text-error">The fetch log could not be loaded.</td>
          </tr>
          <tr v-else-if="!polls.length">
            <td colspan="5" class="text-center opacity-60">
              {{ pending ? 'Loading&hellip;' : 'Not fetched yet.' }}
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </section>
</template>
