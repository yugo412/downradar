<script setup lang="ts">
import type { AdminUser, Role } from '~/types/api'

definePageMeta({ middleware: 'admin', ssr: false })

useSeoMeta({ title: 'Users', robots: 'noindex, nofollow' })

const { user: me } = useAuth()

const { data, pending, error, refresh } = useFetch<{ users: AdminUser[]; count: number }>(
  '/api/v1/users',
)

const users = computed(() => data.value?.users ?? [])

const editing = ref<AdminUser | null>(null)
const removing = ref<AdminUser | null>(null)
const removeBusy = ref(false)
const removeError = ref('')
const actionError = ref('')

function isMe(target: AdminUser) {
  return target.id === me.value?.id
}

function roleClass(role: Role) {
  if (role === 'admin') return 'badge-primary'
  if (role === 'moderator') return 'badge-info'
  return 'badge-ghost'
}

async function setDisabled(target: AdminUser, disabled: boolean) {
  actionError.value = ''

  try {
    await $fetch(`/api/v1/users/${target.id}`, { method: 'PATCH', body: { disabled } })
    await refresh()
  } catch (err) {
    actionError.value = apiError(err)
  }
}

// Deleting takes the services they own with them, because owner_id cascades, so
// the warning has to say so.
function removalMessage(target: AdminUser | null) {
  if (!target) return ''

  const base = `${target.name} (${target.email}) is removed for good, and any session they have ends.`
  if (target.services === 0) return base

  const many = target.services > 1
  return `${base} The ${target.services} service${many ? 's' : ''} they own ${many ? 'are' : 'is'} deleted with them, history included.`
}

async function confirmRemove() {
  if (!removing.value) return

  removeBusy.value = true
  removeError.value = ''

  try {
    await $fetch(`/api/v1/users/${removing.value.id}`, { method: 'DELETE' })
    removing.value = null
    await refresh()
  } catch (err) {
    removeError.value = apiError(err)
  } finally {
    removeBusy.value = false
  }
}

async function onSaved() {
  editing.value = null
  await refresh()
}
</script>

<template>
  <div class="mx-auto max-w-6xl px-4 py-8">
    <header class="mb-6">
      <h1 class="text-2xl font-bold tracking-tight">Users</h1>
      <p class="mt-1 text-sm opacity-70">
        Accounts are created from the shell with <code class="font-mono">downradar-user</code>.
        Here you change what they are, or switch them off.
      </p>
    </header>

    <p v-if="actionError" role="alert" class="alert alert-error mb-4">{{ actionError }}</p>

    <div v-if="error" role="alert" class="alert alert-error">
      Could not load the accounts. Try again in a moment.
    </div>

    <div v-else-if="pending && !users.length" class="flex justify-center py-16">
      <span class="loading loading-spinner" />
    </div>

    <div v-else class="overflow-x-auto rounded-box border border-base-300 bg-base-100 shadow-sm">
      <table class="table">
        <thead>
          <tr>
            <th>Account</th>
            <th>Role</th>
            <th>Services</th>
            <th>Status</th>
            <th class="text-right">Actions</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="u in users" :key="u.id" class="align-top">
            <td>
              <div class="font-medium">
                {{ u.name }}
                <span v-if="isMe(u)" class="badge badge-ghost badge-sm ml-1">you</span>
              </div>
              <div class="text-xs opacity-60">{{ u.email }}</div>
            </td>
            <td>
              <span class="badge badge-sm" :class="roleClass(u.role)">{{ u.role }}</span>
            </td>
            <td class="text-sm opacity-70">{{ u.services }}</td>
            <td>
              <span v-if="u.disabledAt" class="badge badge-error badge-sm">disabled</span>
              <span v-else class="badge badge-success badge-sm">active</span>
            </td>
            <td>
              <div class="flex flex-wrap justify-end gap-1">
                <button class="btn btn-ghost btn-xs" @click="editing = u">Edit</button>
                <button
                  v-if="!isMe(u)"
                  class="btn btn-ghost btn-xs"
                  @click="setDisabled(u, !u.disabledAt)"
                >
                  {{ u.disabledAt ? 'Enable' : 'Disable' }}
                </button>
                <button
                  v-if="!isMe(u)"
                  class="btn btn-ghost btn-xs text-error"
                  @click="removing = u"
                >
                  Delete
                </button>
              </div>
            </td>
          </tr>
          <tr v-if="!users.length">
            <td colspan="5" class="text-center opacity-60">No accounts yet.</td>
          </tr>
        </tbody>
      </table>
    </div>

    <p class="mt-4 text-xs opacity-60">
      Disabling keeps everything and can be undone. Deleting cannot: it also removes the private
      services that account owns. Neither is offered for your own account.
    </p>

    <UserEditor v-if="editing" :user="editing" @close="editing = null" @saved="onSaved" />

    <ConfirmDialog
      v-if="removing"
      heading="Delete account"
      :message="removalMessage(removing)"
      confirm-label="Delete"
      :busy="removeBusy"
      :error="removeError"
      @cancel="removing = null"
      @confirm="confirmRemove"
    />
  </div>
</template>
