<script setup lang="ts">
definePageMeta({ ssr: false })

useSeoMeta({ title: 'Sign in', robots: 'noindex, nofollow' })

const route = useRoute()
const { user, loaded, signIn } = useAuth()

const email = ref('')
const password = ref('')
const busy = ref(false)
const error = ref('')

// Only a path inside this site is accepted, so ?next= cannot bounce a freshly
// signed in moderator to another domain.
const next = computed(() => {
  const raw = String(route.query.next ?? '')
  return raw.startsWith('/') && !raw.startsWith('//') ? raw : '/'
})

// Only reacts to the session arriving, so that signing in does not navigate
// twice: submit() already does it.
watch(
  loaded,
  () => {
    if (loaded.value && user.value) {
      navigateTo(next.value)
    }
  },
  { immediate: true },
)

async function submit() {
  busy.value = true
  error.value = ''

  try {
    await signIn(email.value, password.value)
    await navigateTo(next.value)
  } catch (err) {
    error.value = apiError(err, 'Could not sign in.')
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="mx-auto max-w-md px-4 py-8">
    <h1 class="text-2xl font-bold tracking-tight">Sign in</h1>

    <div class="mt-6 rounded-box border border-base-300 bg-base-100 p-5 shadow-sm">
      <form class="flex flex-col gap-4" @submit.prevent="submit">
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

        <PasswordInput
          v-model="password"
          label="Password"
          autocomplete="current-password"
          required
        />

        <p v-if="error" role="alert" class="text-sm text-error">{{ error }}</p>

        <button type="submit" class="btn btn-primary" :disabled="busy">
          <span v-if="busy" class="loading loading-spinner loading-sm" />
          Sign in
        </button>
      </form>

      <div class="mt-4 flex flex-wrap items-center justify-between gap-2 text-sm opacity-70">
        <NuxtLink to="/forgot" class="link">Forgot your password?</NuxtLink>
        <NuxtLink to="/register" class="link">Create an account</NuxtLink>
      </div>
    </div>
  </div>
</template>
