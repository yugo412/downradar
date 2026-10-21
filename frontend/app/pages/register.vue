<script setup lang="ts">
definePageMeta({ ssr: false })

useSeoMeta({ title: 'Create an account', robots: 'noindex, nofollow' })

const { register } = useAuth()
const { providers, load: loadProviders, signInURL } = useAuthProviders()

onMounted(loadProviders)

const name = ref('')
const email = ref('')
const password = ref('')
const busy = ref(false)
const error = ref('')

async function submit() {
  busy.value = true
  error.value = ''

  try {
    await register(name.value, email.value, password.value)
    await navigateTo('/')
  } catch (err) {
    error.value = apiError(err, 'Could not create the account.')
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="mx-auto max-w-md px-4 py-8">
    <h1 class="text-2xl font-bold tracking-tight">Create an account</h1>
    <p class="mt-1 text-sm opacity-70">
      An account lets you get alerts when the services you care about go down. It is free, and
      there is no newsletter.
    </p>

    <div class="mt-6 rounded-box border border-base-300 bg-base-100 p-5 shadow-sm">
      <template v-if="providers.length">
        <div class="flex flex-col gap-2">
          <a
            v-for="p in providers"
            :key="p.name"
            :href="signInURL(p.name)"
            class="btn btn-outline w-full"
          >
            <ProviderIcon :name="p.name" />
            Continue with {{ p.label }}
          </a>
        </div>
        <div class="divider my-3 text-xs uppercase tracking-wide opacity-60">or</div>
      </template>

      <form class="flex flex-col gap-4" @submit.prevent="submit">
        <label class="form-control w-full">
          <span class="label-text mb-1 text-sm font-medium">Name</span>
          <input
            v-model="name"
            type="text"
            class="input input-bordered w-full"
            autocomplete="name"
            required
            autofocus
          />
        </label>

        <label class="form-control w-full">
          <span class="label-text mb-1 text-sm font-medium">Email</span>
          <input
            v-model="email"
            type="email"
            class="input input-bordered w-full"
            autocomplete="username"
            required
          />
          <span class="label-text-alt mt-1 opacity-70">
            We send a link to it to confirm the address is yours.
          </span>
        </label>

        <PasswordInput
          v-model="password"
          label="Password"
          autocomplete="new-password"
          required
        >
          <span class="label-text-alt mt-1 opacity-70">At least 8 characters.</span>
        </PasswordInput>

        <p v-if="error" role="alert" class="text-sm text-error">{{ error }}</p>

        <button type="submit" class="btn btn-primary" :disabled="busy">
          <span v-if="busy" class="loading loading-spinner loading-sm" />
          Create account
        </button>
      </form>

      <p class="mt-4 text-sm opacity-70">
        Already have an account?
        <NuxtLink to="/login" class="link">Sign in</NuxtLink>
      </p>
    </div>
  </div>
</template>
