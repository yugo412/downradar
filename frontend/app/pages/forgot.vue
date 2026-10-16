<script setup lang="ts">
definePageMeta({ ssr: false })

useSeoMeta({ title: 'Reset your password', robots: 'noindex, nofollow' })

const email = ref('')
const busy = ref(false)
const error = ref('')
const sent = ref(false)

async function submit() {
  busy.value = true
  error.value = ''

  try {
    await $fetch('/api/v1/auth/forgot', { method: 'POST', body: { email: email.value.trim() } })
    sent.value = true
  } catch (err) {
    error.value = apiError(err, 'Could not ask for a reset link.')
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="mx-auto max-w-md px-4 py-8">
    <h1 class="text-2xl font-bold tracking-tight">Forgot your password?</h1>
    <p class="mt-1 text-sm opacity-70">
      Tell us the address on the account and we will send a link to choose a new password.
    </p>

    <div class="mt-6 rounded-box border border-base-300 bg-base-100 p-5 shadow-sm">
      <p v-if="sent" class="text-sm">
        If that address has an account, a link is on its way. It works once and expires in an hour.
      </p>

      <form v-else class="flex flex-col gap-4" @submit.prevent="submit">
        <label class="form-control w-full">
          <span class="label-text mb-1 text-sm font-medium">Email</span>
          <input
            v-model="email"
            type="email"
            class="input input-bordered w-full"
            autocomplete="username"
            required
            autofocus
          />
        </label>

        <p v-if="error" role="alert" class="text-sm text-error">{{ error }}</p>

        <button type="submit" class="btn btn-primary" :disabled="busy">
          <span v-if="busy" class="loading loading-spinner loading-sm" />
          Send the link
        </button>
      </form>

      <p class="mt-4 text-sm opacity-70">
        <NuxtLink to="/login" class="link">Back to sign in</NuxtLink>
      </p>
    </div>
  </div>
</template>
