<script setup lang="ts">
import type { LinkedLogin } from '~/types/api'

definePageMeta({ middleware: 'authenticated', ssr: false })

useSeoMeta({ title: 'Linked logins', robots: 'noindex, nofollow' })

const route = useRoute()
const { user } = useAuth()

const providers = ref<LinkedLogin[]>([])
const hasPassword = ref(user.value?.hasPassword ?? true)
const loading = ref(true)
const error = ref('')
const notice = ref('')

const removing = ref<LinkedLogin | null>(null)
const removeBusy = ref(false)
const removeError = ref('')

const connectedCount = computed(() => providers.value.filter((p) => p.connected).length)

const providerLabel = computed(() => {
  const name = String(route.query.provider ?? '')
  return providers.value.find((p) => p.name === name)?.label ?? 'the provider'
})

const linkedNotice = computed(() => {
  const name = String(route.query.linked ?? '')
  if (!name) return ''
  return `${providers.value.find((p) => p.name === name)?.label ?? name} is now connected.`
})

const linkError = computed(() => {
  const reason = String(route.query.oauth ?? '')
  if (!reason) return ''
  if (reason === 'refused') return `Connecting ${providerLabel.value} was cancelled.`
  if (reason === 'taken') {
    return `That ${providerLabel.value} account is already connected to another DownRadar account, so it was not added here.`
  }
  if (reason === 'address') {
    return `${providerLabel.value} did not give an email address that can be used here.`
  }
  if (reason === 'session') {
    return 'Your session ended before the login could be connected. Try again.'
  }
  return `Connecting ${providerLabel.value} did not work. Try again.`
})

const shownError = computed(() => error.value || linkError.value)
const shownNotice = computed(() => notice.value || linkedNotice.value)

async function load() {
  loading.value = true
  try {
    const res = await $fetch<{ hasPassword: boolean; providers: LinkedLogin[] }>(
      '/api/v1/auth/logins',
    )
    providers.value = res.providers ?? []
    hasPassword.value = res.hasPassword
  } catch (err) {
    error.value = apiError(err, 'Could not load the logins connected to this account.')
  } finally {
    loading.value = false
  }
}

onMounted(load)

function connectURL(name: string) {
  return `/api/v1/auth/${encodeURIComponent(name)}/link`
}

// A provider can go when a password remains, or when another provider does.
function canRemove(provider: LinkedLogin) {
  return hasPassword.value || connectedCount.value > 1
}

function askRemove(provider: LinkedLogin) {
  removeError.value = ''
  removing.value = provider
}

function cancelRemove() {
  if (removeBusy.value) return
  removing.value = null
  removeError.value = ''
}

async function removeLogin() {
  if (!removing.value) return

  const provider = removing.value
  removeBusy.value = true
  removeError.value = ''

  try {
    await $fetch(`/api/v1/auth/logins/${encodeURIComponent(provider.name)}`, {
      method: 'DELETE',
    })
    provider.connected = false
    provider.email = ''
    provider.username = ''
    notice.value = `${provider.label} was disconnected.`
    removing.value = null
  } catch (err) {
    removeError.value = apiError(err, `Could not disconnect ${provider.label}.`)
  } finally {
    removeBusy.value = false
  }
}
</script>

<template>
  <div class="mx-auto max-w-2xl px-4 py-8">
    <h1 class="text-2xl font-bold tracking-tight">Linked logins</h1>
    <p class="mt-1 text-sm opacity-70">
      These accounts can sign in to your DownRadar account. Connecting one here adds it to this
      account; it does not make another account.
    </p>

    <p v-if="shownError" role="alert" class="mt-4 text-sm text-error">{{ shownError }}</p>
    <p v-if="shownNotice" role="status" class="mt-4 text-sm text-success">{{ shownNotice }}</p>

    <div v-if="loading" class="mt-8 flex justify-center">
      <span class="loading loading-spinner" />
    </div>

    <p
      v-else-if="!providers.length && !error"
      class="mt-6 rounded-box border border-base-300 bg-base-100 p-5 text-sm opacity-70 shadow-sm"
    >
      No provider sign in is configured on this site.
    </p>

    <div v-else-if="providers.length" class="mt-6 flex flex-col gap-3">
      <div
        v-for="p in providers"
        :key="p.name"
        class="rounded-box border border-base-300 bg-base-100 p-5 shadow-sm"
      >
        <div class="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
          <div class="flex items-start gap-3">
            <span
              class="mt-0.5 flex size-8 shrink-0 items-center justify-center rounded-full bg-base-200"
            >
              <ProviderIcon :name="p.name" class="size-4" />
            </span>
            <div>
              <div class="flex flex-wrap items-center gap-2">
                <span class="font-medium">{{ p.label }}</span>
                <span v-if="p.connected" class="badge badge-success badge-sm">Connected</span>
                <span v-else class="badge badge-ghost badge-sm">Not connected</span>
              </div>
              <p v-if="p.connected" class="mt-1 text-sm opacity-70">
                <template v-if="p.username">{{ p.username }}</template>
                <template v-if="p.username && p.email"> · </template>
                <template v-if="p.email">{{ p.email }}</template>
              </p>
              <p v-else class="mt-1 text-sm opacity-70">
                Sign in to this account with {{ p.label }}.
              </p>
            </div>
          </div>

          <div class="flex shrink-0 items-center gap-2">
            <template v-if="p.connected">
              <button
                v-if="canRemove(p)"
                type="button"
                class="btn btn-outline btn-error btn-sm"
                @click="askRemove(p)"
              >
                Remove
              </button>
              <NuxtLink v-else to="/account/password" class="btn btn-outline btn-sm">
                Set a password
              </NuxtLink>
            </template>
            <a v-else :href="connectURL(p.name)" class="btn btn-outline btn-sm">Connect</a>
          </div>
        </div>

        <p v-if="p.connected && !canRemove(p)" class="mt-3 text-sm opacity-70">
          This is the only login connected to your account. Set a password before removing it.
        </p>
      </div>
    </div>

    <ConfirmDialog
      v-if="removing"
      :heading="`Remove ${removing.label} login?`"
      :message="`You will no longer be able to sign in with ${removing.label}. Your account and its alerts stay as they are.`"
      confirm-label="Remove"
      :busy="removeBusy"
      :error="removeError"
      @cancel="cancelRemove"
      @confirm="removeLogin"
    />
  </div>
</template>
