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
const busy = ref<number | null>(null)
const deleting = ref<AccountAlert | null>(null)

const atLimit = computed(() => limit.value !== null && used.value >= limit.value)

onMounted(load)

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
</script>

<template>
  <div class="mx-auto max-w-6xl px-4 py-8">
    <header class="mb-6">
      <h1 class="text-2xl font-bold tracking-tight">Alerts</h1>
      <p class="mt-1 text-sm opacity-70">
        You get an email each time one of these services changes status. Every email carries an
        unsubscribe link as well.
      </p>
    </header>

    <p v-if="error" role="alert" class="alert alert-error mb-6">
      <span>{{ error }}</span>
    </p>

    <p v-if="atLimit" class="alert alert-warning mb-6">
      <span>
        Your account has reached its limit of {{ limit }} alerts. Remove one before adding another.
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
      <!-- A phone cannot hold five columns without scrolling sideways, so the
           table is for wide screens and the same rows are cards below. -->
      <div class="hidden overflow-x-auto rounded-box border border-base-300 bg-base-100 shadow-sm md:block">
        <table class="table">
          <thead>
            <tr>
              <th>Service</th>
              <th>Address</th>
              <th>Added</th>
              <th>State</th>
              <th class="text-right">Actions</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="alert in alerts" :key="alert.id" class="align-top">
              <td>
                <NuxtLink :to="`/${alert.vendorSlug}`" class="link font-medium">
                  {{ alert.vendorName }}
                </NuxtLink>
                <div class="mt-1">
                  <StatusBadge :status="alert.vendorStatus" size="xs" />
                </div>
              </td>
              <td class="text-sm break-all opacity-70">{{ alert.email }}</td>
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
          v-for="alert in alerts"
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
            </div>
            <span class="badge badge-sm shrink-0 whitespace-nowrap" :class="stateOf(alert).cls">
              {{ stateOf(alert).label }}
            </span>
          </div>

          <p class="mt-2 text-sm break-all opacity-70">{{ alert.email }}</p>
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
    </template>

    <ConfirmDialog
      v-if="deleting"
      heading="Delete this alert?"
      :message="`You will not get any more emails about ${deleting.vendorName}, and the row disappears from this list.`"
      confirm-label="Delete"
      :busy="busy === deleting.id"
      :error="error"
      @confirm="remove(deleting)"
      @cancel="deleting = null"
    />
  </div>
</template>
