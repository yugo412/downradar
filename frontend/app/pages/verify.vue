<script setup lang="ts">
// Same reasoning as the unsubscribe page: the link opens a page that asks, so a
// mail scanner that fetches it cannot spend the token on its own.
definePageMeta({ ssr: false })

useSeoMeta({ title: 'Confirm your email', robots: 'noindex, nofollow' })

const { load } = useAuth()

const route = useRoute()
const token = computed(() => String(route.query.token ?? ''))

const busy = ref(false)
const error = ref('')
const confirmed = ref(false)

async function confirm() {
  busy.value = true
  error.value = ''

  try {
    await $fetch('/api/v1/auth/verify', { method: 'POST', body: { token: token.value } })
    confirmed.value = true
    // A signed in visitor's own copy of the account is now out of date.
    await load()
  } catch (err) {
    error.value = apiError(err, 'Could not confirm this address.')
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="mx-auto max-w-md px-4 py-8">
    <h1 class="text-2xl font-bold tracking-tight">Confirm your email</h1>

    <div class="mt-6 rounded-box border border-base-300 bg-base-100 p-5 shadow-sm">
      <p v-if="!token" class="text-sm opacity-80">
        This link has no token in it. Open the link from the email again.
      </p>

      <template v-else-if="confirmed">
        <p class="text-sm">
          That address is confirmed. Thank you.
        </p>
        <NuxtLink to="/" class="btn btn-primary btn-sm mt-4">All services</NuxtLink>
      </template>

      <template v-else>
        <p class="text-sm opacity-80">
          Confirm that this address belongs to you, so we know where to send alerts.
        </p>
        <p v-if="error" role="alert" class="mt-3 text-sm text-error">{{ error }}</p>
        <button type="button" class="btn btn-primary btn-sm mt-4" :disabled="busy" @click="confirm">
          <span v-if="busy" class="loading loading-spinner loading-xs" />
          Confirm this address
        </button>
      </template>
    </div>
  </div>
</template>
