<script setup lang="ts">
definePageMeta({ middleware: 'authenticated', ssr: false })

useSeoMeta({ title: 'Edit profile', robots: 'noindex, nofollow' })

const { user, updateProfile, deleteAccount } = useAuth()

// Staff keep access to the site's own screens, so they cannot remove the account
// that holds it. An account made through a provider has no password yet, and
// there is nothing to confirm a deletion with until one is set.
const isStaff = computed(
  () => user.value?.role === 'admin' || user.value?.role === 'moderator',
)
const hasPassword = computed(() => user.value?.hasPassword ?? true)

const name = ref(user.value?.name ?? '')
const email = ref(user.value?.email ?? '')
const busy = ref(false)
const error = ref('')
const saved = ref(false)

const password = ref('')
const confirming = ref(false)
const removeBusy = ref(false)
const removeError = ref('')

async function save() {
  busy.value = true
  error.value = ''
  saved.value = false

  try {
    await updateProfile(name.value, email.value.trim())
    saved.value = true
  } catch (err) {
    error.value = apiError(err)
  } finally {
    busy.value = false
  }
}

function askRemove(event: Event) {
  // A password manager can fill the field without firing the input event that
  // v-model listens to, leaving the ref empty even though the field looks full.
  // Read what the browser actually has before asking for confirmation.
  const form = event.currentTarget as HTMLFormElement
  const field = form.elements.namedItem('password')
  if (field instanceof HTMLInputElement) password.value = field.value

  removeError.value = ''
  confirming.value = true
}

function cancelRemove() {
  confirming.value = false
  removeError.value = ''
  password.value = ''
}

async function removeAccount() {
  removeBusy.value = true
  removeError.value = ''

  try {
    await deleteAccount(password.value)
    confirming.value = false
    await navigateTo('/')
  } catch (err) {
    removeError.value = apiError(err)
  } finally {
    removeBusy.value = false
  }
}
</script>

<template>
  <div class="mx-auto max-w-2xl px-4 py-8">
    <h1 class="text-2xl font-bold tracking-tight">Edit profile</h1>
    <p class="mt-1 text-sm opacity-70">The name is shown next to your sessions and actions.</p>

    <div class="mt-6 rounded-box border border-base-300 bg-base-100 p-5 shadow-sm">
      <form class="flex flex-col gap-4" @submit.prevent="save">
        <label class="form-control w-full">
          <span class="label-text mb-1 text-sm font-medium">Name</span>
          <input
            v-model="name"
            type="text"
            class="input input-bordered w-full"
            maxlength="80"
            required
          />
        </label>

        <label class="form-control w-full">
          <span class="label-text mb-1 text-sm font-medium">Email</span>
          <input
            v-model="email"
            type="email"
            class="input input-bordered w-full"
            autocomplete="email"
            required
          />
          <span class="label-text-alt mt-1 opacity-60">
            Changing it sends a new confirmation link to the new address, and the old one stops
            being confirmed.
          </span>
        </label>

        <p v-if="error" role="alert" class="text-sm text-error">{{ error }}</p>
        <p v-if="saved" role="status" class="text-sm text-success">Your profile was saved.</p>

        <div class="flex justify-end">
          <button type="submit" class="btn btn-primary btn-sm" :disabled="busy">
            <span v-if="busy" class="loading loading-spinner loading-xs" />
            Save
          </button>
        </div>
      </form>
    </div>

    <div class="mt-8 rounded-box border border-error/40 bg-base-100 p-5 shadow-sm">
      <h2 class="text-lg font-semibold text-error">Delete account</h2>
      <p class="mt-2 text-sm opacity-80">
        This cannot be undone. Your alerts, channels, delivery records, limits, sessions and
        sign-in links are removed with the account. Services you added stay in the catalogue
        without your name attached.
      </p>

      <p v-if="isStaff" class="mt-3 text-sm opacity-70">
        Staff accounts cannot delete themselves, so the site always has somebody who can manage it.
      </p>

      <p v-else-if="!hasPassword" class="mt-3 text-sm opacity-70">
        This account signs in through a provider and has no password yet.
        <NuxtLink to="/account/password" class="link">Set a password</NuxtLink>
        first, then come back here to delete the account.
      </p>

      <form v-else class="mt-4 flex flex-col gap-4" @submit.prevent="askRemove">
        <PasswordInput
          v-model="password"
          name="password"
          label="Password"
          autocomplete="current-password"
          required
        />
        <div class="flex justify-end">
          <button type="submit" class="btn btn-error btn-sm" :disabled="removeBusy">
            Delete account
          </button>
        </div>
      </form>
    </div>

    <ConfirmDialog
      v-if="confirming"
      heading="Delete your account?"
      message="Your account is removed for good. This cannot be undone."
      confirm-label="Delete account"
      :busy="removeBusy"
      :error="removeError"
      @cancel="cancelRemove"
      @confirm="removeAccount"
    />
  </div>
</template>
