<script setup lang="ts">
// The link in the email opens this page rather than setting the password by
// itself: mail clients and scanners fetch the links they are sent, and a link
// that acted on a fetch would spend itself before its owner saw it.
definePageMeta({ ssr: false })

useSeoMeta({ title: 'Choose a new password', robots: 'noindex, nofollow' })

const { load } = useAuth()

const route = useRoute()
const token = computed(() => String(route.query.token ?? ''))

const password = ref('')
const busy = ref(false)
const error = ref('')
const done = ref(false)

async function submit() {
  busy.value = true
  error.value = ''

  try {
    await $fetch('/api/v1/auth/reset', {
      method: 'POST',
      body: { token: token.value, password: password.value },
    })
    done.value = true
    // The reset ended every session, so the browser's copy of the account is
    // stale until it asks again.
    await load()
  } catch (err) {
    error.value = apiError(err, 'Could not set the new password.')
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="mx-auto max-w-md px-4 py-8">
    <h1 class="text-2xl font-bold tracking-tight">Choose a new password</h1>

    <div class="mt-6 rounded-box border border-base-300 bg-base-100 p-5 shadow-sm">
      <p v-if="!token" class="text-sm opacity-80">
        This link has no token in it. Open the reset link from the email again.
      </p>

      <template v-else-if="done">
        <p class="text-sm">
          Your password is changed, and every other session was signed out. You can sign in with
          the new one now.
        </p>
        <NuxtLink to="/login" class="btn btn-primary btn-sm mt-4">Sign in</NuxtLink>
      </template>

      <form v-else class="flex flex-col gap-4" @submit.prevent="submit">
        <PasswordInput
          v-model="password"
          label="New password"
          autocomplete="new-password"
          required
          autofocus
        >
          <span class="label-text-alt mt-1 opacity-70">At least 8 characters.</span>
        </PasswordInput>

        <p v-if="error" role="alert" class="text-sm text-error">{{ error }}</p>

        <button type="submit" class="btn btn-primary" :disabled="busy">
          <span v-if="busy" class="loading loading-spinner loading-sm" />
          Set the password
        </button>
      </form>
    </div>
  </div>
</template>
