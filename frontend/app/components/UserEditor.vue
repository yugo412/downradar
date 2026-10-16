<script setup lang="ts">
import type { AdminUser, Role } from '~/types/api'

const props = defineProps<{ user: AdminUser }>()

const emit = defineEmits<{
  close: []
  saved: []
}>()

const name = ref(props.user.name)
const email = ref(props.user.email)
const role = ref<Role>(props.user.role)
const busy = ref(false)
const error = ref('')

const roles: { value: Role; label: string }[] = [
  { value: 'admin', label: 'admin — everything, including people' },
  { value: 'moderator', label: 'moderator — the service catalogue' },
  { value: 'subscriber', label: 'subscriber — services of their own' },
]

async function save() {
  busy.value = true
  error.value = ''

  try {
    await $fetch(`/api/v1/users/${props.user.id}`, {
      method: 'PATCH',
      body: { name: name.value, email: email.value, role: role.value },
    })
    emit('saved')
  } catch (err) {
    error.value = apiError(err)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div
    class="fixed inset-0 z-50 overflow-y-auto bg-black/50 p-4"
    role="dialog"
    aria-modal="true"
    @click.self="emit('close')"
  >
    <div class="mx-auto my-8 w-full max-w-lg rounded-box border border-base-300 bg-base-100 shadow-xl">
      <div class="flex items-center justify-between border-b border-base-300 px-5 py-3">
        <h2 class="text-lg font-semibold">Edit {{ user.name }}</h2>
        <button type="button" class="btn btn-ghost btn-sm" aria-label="Close" @click="emit('close')">
          ✕
        </button>
      </div>

      <form class="flex flex-col gap-4 p-5" @submit.prevent="save">
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
          <input v-model="email" type="email" class="input input-bordered w-full" required />
          <span class="label-text-alt mt-1 opacity-60">
            This is the address they sign in with, so changing it changes their login.
          </span>
        </label>

        <label class="form-control w-full">
          <span class="label-text mb-1 text-sm font-medium">Role</span>
          <select v-model="role" class="select select-bordered w-full">
            <option v-for="r in roles" :key="r.value" :value="r.value">{{ r.label }}</option>
          </select>
        </label>

        <p v-if="error" role="alert" class="text-sm text-error">{{ error }}</p>

        <div class="flex items-center justify-end gap-2">
          <button type="button" class="btn btn-ghost btn-sm" :disabled="busy" @click="emit('close')">
            Cancel
          </button>
          <button type="submit" class="btn btn-primary btn-sm" :disabled="busy">
            <span v-if="busy" class="loading loading-spinner loading-xs" />
            Save changes
          </button>
        </div>
      </form>
    </div>
  </div>
</template>
