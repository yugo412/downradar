<script setup lang="ts">
definePageMeta({ middleware: 'authenticated', ssr: false })

useSeoMeta({ title: 'Edit profile', robots: 'noindex, nofollow' })

const { user, updateProfile } = useAuth()

const name = ref(user.value?.name ?? '')
const email = ref(user.value?.email ?? '')
const busy = ref(false)
const error = ref('')
const saved = ref(false)

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
  </div>
</template>
