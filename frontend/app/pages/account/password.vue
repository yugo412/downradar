<script setup lang="ts">
definePageMeta({ middleware: 'authenticated', ssr: false })

const { user, changePassword } = useAuth()

// An account made through a provider has no password yet, so the screen asks
// for a new one only and the API has nothing to compare. Until the account is
// known, the conservative answer is that there is one to change.
const hasPassword = computed(() => user.value?.hasPassword ?? true)

useSeoMeta({
  title: () => (hasPassword.value ? 'Change password' : 'Set a password'),
  robots: 'noindex, nofollow',
})

const currentPassword = ref('')
const newPassword = ref('')
const confirmation = ref('')
const busy = ref(false)
const error = ref('')
const saved = ref('')
const otherSessionsEnded = ref(0)

const mismatch = computed(
  () => confirmation.value !== '' && confirmation.value !== newPassword.value,
)

async function save() {
  if (mismatch.value) {
    error.value = 'The two new passwords do not match.'
    return
  }

  const firstPassword = !hasPassword.value
  busy.value = true
  error.value = ''
  saved.value = ''

  try {
    const res = await changePassword(currentPassword.value, newPassword.value)
    otherSessionsEnded.value = res.otherSessionsEnded
    saved.value = firstPassword ? 'Your password was set.' : 'Your password was changed.'
    currentPassword.value = ''
    newPassword.value = ''
    confirmation.value = ''
  } catch (err) {
    error.value = apiError(err)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="mx-auto max-w-2xl px-4 py-8">
    <h1 class="text-2xl font-bold tracking-tight">
      {{ hasPassword ? 'Change password' : 'Set a password' }}
    </h1>
    <p class="mt-1 text-sm opacity-70">
      <template v-if="hasPassword">
        Changing it signs out every other device. This browser stays signed in.
      </template>
      <template v-else>
        This account was made with a provider and has no password yet. Setting one lets you sign in
        with your address as well. This browser stays signed in.
      </template>
    </p>

    <div class="mt-6 rounded-box border border-base-300 bg-base-100 p-5 shadow-sm">
      <form class="flex flex-col gap-4" @submit.prevent="save">
        <PasswordInput
          v-if="hasPassword"
          v-model="currentPassword"
          label="Current password"
          autocomplete="current-password"
          required
        />

        <PasswordInput
          v-model="newPassword"
          label="New password"
          autocomplete="new-password"
          minlength="8"
          required
        >
          <span class="label-text-alt mt-1 opacity-60">At least 8 characters.</span>
        </PasswordInput>

        <PasswordInput
          v-model="confirmation"
          label="Repeat the new password"
          autocomplete="new-password"
          minlength="8"
          required
        >
          <span v-if="mismatch" class="label-text-alt mt-1 text-error">
            Does not match the new password.
          </span>
        </PasswordInput>

        <p v-if="error" role="alert" class="text-sm text-error">{{ error }}</p>
        <p v-if="saved" role="status" class="text-sm text-success">
          {{ saved }}
          <span v-if="otherSessionsEnded > 0">
            {{ otherSessionsEnded }} other session{{ otherSessionsEnded > 1 ? 's were' : ' was' }}
            signed out.
          </span>
        </p>

        <div class="flex justify-end">
          <button type="submit" class="btn btn-primary btn-sm" :disabled="busy || mismatch">
            <span v-if="busy" class="loading loading-spinner loading-xs" />
            {{ hasPassword ? 'Change password' : 'Set password' }}
          </button>
        </div>
      </form>
    </div>
  </div>
</template>
