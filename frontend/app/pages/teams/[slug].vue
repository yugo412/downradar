<script setup lang="ts">
import type { Team, TeamInvitation, TeamMember } from '~/types/api'

definePageMeta({ middleware: 'authenticated', ssr: false })

useSeoMeta({ title: 'Team', robots: 'noindex, nofollow' })

const route = useRoute()
const teamSlug = computed(() => String(route.params.slug ?? ''))

const teams = ref<Team[]>([])
const team = ref<Team | null>(null)
const members = ref<TeamMember[]>([])
const invitations = ref<TeamInvitation[]>([])
const loading = ref(true)
const error = ref('')

const isOwner = computed(() => team.value?.role === 'owner')
const canManageMembers = computed(
  () => team.value?.role === 'owner' || team.value?.role === 'manager',
)

const roleLabel: Record<string, string> = {
  owner: 'Owner',
  manager: 'Manager',
  member: 'Member',
}

// A manager can only hand out the member role; inviting another manager is the
// owner's call.
const inviteRoles = computed(() => (isOwner.value ? ['manager', 'member'] : ['member']))

const inviteEmail = ref('')
const inviteRole = ref('member')
const inviting = ref(false)
const inviteError = ref('')
const inviteNotice = ref('')

const name = ref('')
const renaming = ref(false)
const renameError = ref('')
const renameNotice = ref('')

const changingRole = ref<number | null>(null)
const resending = ref<number | null>(null)
const revoking = ref<number | null>(null)
const notice = ref('')

const removing = ref<TeamMember | null>(null)
const removeBusy = ref(false)
const removeError = ref('')

const deleting = ref(false)
const deleteBusy = ref(false)
const deleteError = ref('')

async function load() {
  loading.value = true
  error.value = ''
  try {
    const [list, detail] = await Promise.all([
      $fetch<{ teams: Team[] }>('/api/v1/teams'),
      $fetch<{
        team: Team
        members: TeamMember[]
        invitations: TeamInvitation[]
      }>(`/api/v1/teams/${encodeURIComponent(teamSlug.value)}`),
    ])
    teams.value = list.teams ?? []
    team.value = detail.team
    members.value = detail.members ?? []
    invitations.value = detail.invitations ?? []
    name.value = detail.team.name
    if (!inviteRoles.value.includes(inviteRole.value)) {
      inviteRole.value = inviteRoles.value[0] ?? 'member'
    }
  } catch (err) {
    team.value = null
    error.value = apiError(err, 'Could not load this team.')
  } finally {
    loading.value = false
  }
}

async function invite() {
  inviting.value = true
  inviteError.value = ''
  inviteNotice.value = ''
  try {
    const res = await $fetch<{ invitation: TeamInvitation; sent: boolean }>(
      `/api/v1/teams/${encodeURIComponent(teamSlug.value)}/invitations`,
      {
        method: 'POST',
        body: { email: inviteEmail.value.trim(), role: inviteRole.value },
      },
    )
    inviteEmail.value = ''
    inviteNotice.value = res.sent
      ? 'Invitation sent.'
      : 'The invitation was saved, but the email could not be sent. Try resending it.'
    await load()
  } catch (err) {
    inviteError.value = apiError(err, 'Could not send the invitation.')
  } finally {
    inviting.value = false
  }
}

async function resend(invitation: TeamInvitation) {
  resending.value = invitation.id
  notice.value = ''
  try {
    const res = await $fetch<{ sent: boolean }>(
      `/api/v1/teams/${encodeURIComponent(teamSlug.value)}/invitations/${invitation.id}/resend`,
      { method: 'POST' },
    )
    notice.value = res.sent
      ? 'Invitation sent again.'
      : 'The invitation was saved, but the email could not be sent.'
    await load()
  } catch (err) {
    notice.value = apiError(err, 'Could not resend the invitation.')
  } finally {
    resending.value = null
  }
}

async function revoke(invitation: TeamInvitation) {
  revoking.value = invitation.id
  notice.value = ''
  try {
    await $fetch(
      `/api/v1/teams/${encodeURIComponent(teamSlug.value)}/invitations/${invitation.id}`,
      { method: 'DELETE' },
    )
    notice.value = 'Invitation revoked.'
    await load()
  } catch (err) {
    notice.value = apiError(err, 'Could not revoke the invitation.')
  } finally {
    revoking.value = null
  }
}

function canManageInvitation(invitation: TeamInvitation) {
  return isOwner.value || invitation.role === 'member'
}

function canRemoveMember(member: TeamMember) {
  if (member.role === 'owner') return false
  if (isOwner.value) return true
  return team.value?.role === 'manager' && member.role === 'member'
}

async function changeRole(member: TeamMember, event: Event) {
  const role = (event.target as HTMLSelectElement).value
  changingRole.value = member.userId
  notice.value = ''
  try {
    await $fetch(`/api/v1/teams/${encodeURIComponent(teamSlug.value)}/members/${member.userId}`, {
      method: 'PATCH',
      body: { role },
    })
    await load()
  } catch (err) {
    notice.value = apiError(err, 'Could not change that role.')
    await load()
  } finally {
    changingRole.value = null
  }
}

function askRemove(member: TeamMember) {
  removeError.value = ''
  removing.value = member
}

function cancelRemove() {
  if (removeBusy.value) return
  removing.value = null
  removeError.value = ''
}

async function removeMember() {
  if (!removing.value) return
  removeBusy.value = true
  removeError.value = ''
  try {
    await $fetch(
      `/api/v1/teams/${encodeURIComponent(teamSlug.value)}/members/${removing.value.userId}`,
      { method: 'DELETE' },
    )
    removing.value = null
    await load()
  } catch (err) {
    removeError.value = apiError(err, 'Could not remove that member.')
  } finally {
    removeBusy.value = false
  }
}

async function rename() {
  renaming.value = true
  renameError.value = ''
  renameNotice.value = ''
  try {
    await $fetch(`/api/v1/teams/${encodeURIComponent(teamSlug.value)}`, {
      method: 'PATCH',
      body: { name: name.value.trim() },
    })
    renameNotice.value = 'Team name saved.'
    await load()
  } catch (err) {
    renameError.value = apiError(err, 'Could not save the team name.')
  } finally {
    renaming.value = false
  }
}

function askDelete() {
  deleteError.value = ''
  deleting.value = true
}

async function deleteTeam() {
  deleteBusy.value = true
  deleteError.value = ''
  try {
    await $fetch(`/api/v1/teams/${encodeURIComponent(teamSlug.value)}`, { method: 'DELETE' })
    await navigateTo('/teams')
  } catch (err) {
    deleteError.value = apiError(err, 'Could not delete the team.')
  } finally {
    deleteBusy.value = false
  }
}

function expired(invitation: TeamInvitation) {
  return Date.parse(invitation.expiresAt) <= Date.now()
}

onMounted(load)
watch(teamSlug, load)
</script>

<template>
  <div class="mx-auto max-w-6xl px-4 py-8">
    <NuxtLink to="/teams" class="link link-hover text-sm opacity-70">&larr; Teams</NuxtLink>

    <header v-if="team" class="mt-4">
      <div class="flex flex-wrap items-center gap-3">
        <h1 class="text-2xl font-bold tracking-tight">{{ team.name }}</h1>
        <span class="badge badge-sm" :class="isOwner ? 'badge-primary' : 'badge-ghost'">
          {{ roleLabel[team.role] }}
        </span>
      </div>
      <p class="mt-1 text-sm opacity-70">
        Members and invitations for this workspace, and the services and alerts
        it owns.
      </p>
      <div class="mt-3 flex flex-wrap gap-2">
        <NuxtLink to="/services" class="btn btn-ghost btn-sm">Team services</NuxtLink>
        <NuxtLink :to="`/alerts?scope=${team.id}`" class="btn btn-ghost btn-sm">
          Team alerts
        </NuxtLink>
      </div>
    </header>

    <TeamTabs :teams="teams" :current="teamSlug" class="mt-6">
      <p v-if="error" role="alert" class="alert alert-error mb-4">{{ error }}</p>

      <div v-else-if="loading" class="flex items-center gap-2 text-sm opacity-70">
        <span class="loading loading-spinner loading-sm" />
        Loading the team…
      </div>

      <template v-else-if="team">
      <p v-if="notice" role="status" class="alert alert-info mb-4 py-2 text-sm">{{ notice }}</p>

      <section>
        <div class="flex flex-wrap items-center justify-between gap-2">
          <h2 class="text-lg font-semibold">Members</h2>
          <span class="text-xs opacity-60">{{ members.length }} member{{ members.length === 1 ? '' : 's' }}</span>
        </div>

        <ul class="mt-3 divide-y divide-base-300">
          <li
            v-for="member in members"
            :key="member.userId"
            class="flex flex-wrap items-center justify-between gap-3 py-3"
          >
            <div class="min-w-0">
              <p class="font-medium">{{ member.name }}</p>
              <p class="text-xs break-all opacity-60">{{ member.email }}</p>
            </div>
            <div class="flex items-center gap-2">
              <select
                v-if="isOwner && member.role !== 'owner'"
                class="select select-bordered select-xs"
                :value="member.role"
                :disabled="changingRole === member.userId"
                :aria-label="`Role for ${member.name}`"
                @change="changeRole(member, $event)"
              >
                <option value="manager">Manager</option>
                <option value="member">Member</option>
              </select>
              <span
                v-else
                class="badge badge-sm"
                :class="member.role === 'owner' ? 'badge-primary' : 'badge-ghost'"
              >
                {{ roleLabel[member.role] }}
              </span>
              <button
                v-if="canRemoveMember(member)"
                type="button"
                class="btn btn-ghost btn-xs text-error"
                @click="askRemove(member)"
              >
                Remove
              </button>
            </div>
          </li>
        </ul>
      </section>

      <section v-if="canManageMembers" class="mt-6 border-t border-base-300 pt-6">
        <h2 class="text-lg font-semibold">Invite someone</h2>
        <p class="mt-1 text-sm opacity-70">
          The address gets an email with a link to join. If they do not have an
          account yet, the link lets them create one.
        </p>
        <form class="mt-4 flex flex-col gap-3 sm:flex-row" @submit.prevent="invite">
          <input
            v-model="inviteEmail"
            type="email"
            class="input input-bordered w-full"
            placeholder="person@example.com"
            aria-label="Email address"
            autocomplete="email"
            required
          />
          <select v-model="inviteRole" class="select select-bordered shrink-0" aria-label="Role">
            <option v-for="role in inviteRoles" :key="role" :value="role">
              {{ roleLabel[role] }}
            </option>
          </select>
          <button type="submit" class="btn btn-primary shrink-0" :disabled="inviting">
            <span v-if="inviting" class="loading loading-spinner loading-xs" />
            Send invitation
          </button>
        </form>
        <p v-if="inviteError" role="alert" class="mt-3 text-sm text-error">{{ inviteError }}</p>
        <p v-if="inviteNotice" role="status" class="mt-3 text-sm text-success">{{ inviteNotice }}</p>

        <div class="mt-6">
          <h3 class="text-sm font-semibold">Pending invitations</h3>
          <p v-if="!invitations.length" class="mt-2 text-sm opacity-70">No pending invitations.</p>
          <ul v-else class="mt-2 divide-y divide-base-300">
            <li
              v-for="invitation in invitations"
              :key="invitation.id"
              class="flex flex-wrap items-center justify-between gap-3 py-3"
            >
              <div class="min-w-0">
                <p class="break-all font-medium">{{ invitation.email }}</p>
                <p class="text-xs opacity-60">
                  {{ roleLabel[invitation.role] }} ·
                  <template v-if="expired(invitation)">expired</template>
                  <template v-else>expires <LocalTime :at="invitation.expiresAt" /></template>
                </p>
              </div>
              <div v-if="canManageInvitation(invitation)" class="flex items-center gap-1">
                <button
                  type="button"
                  class="btn btn-ghost btn-xs"
                  :disabled="resending === invitation.id"
                  @click="resend(invitation)"
                >
                  <span v-if="resending === invitation.id" class="loading loading-spinner loading-xs" />
                  Resend
                </button>
                <button
                  type="button"
                  class="btn btn-ghost btn-xs text-error"
                  :disabled="revoking === invitation.id"
                  @click="revoke(invitation)"
                >
                  Revoke
                </button>
              </div>
            </li>
          </ul>
        </div>
      </section>

      <section v-if="isOwner" class="mt-6 border-t border-base-300 pt-6">
        <h2 class="text-lg font-semibold">Team settings</h2>
        <form class="mt-4 flex flex-col gap-3 sm:flex-row" @submit.prevent="rename">
          <input
            v-model="name"
            type="text"
            class="input input-bordered w-full"
            aria-label="Team name"
            maxlength="80"
            required
          />
          <button type="submit" class="btn shrink-0" :disabled="renaming">
            <span v-if="renaming" class="loading loading-spinner loading-xs" />
            Save name
          </button>
        </form>
        <p v-if="renameError" role="alert" class="mt-3 text-sm text-error">{{ renameError }}</p>
        <p v-if="renameNotice" role="status" class="mt-3 text-sm text-success">{{ renameNotice }}</p>

        <div class="mt-6 border-t border-base-300 pt-4">
          <h3 class="text-sm font-semibold text-error">Delete team</h3>
          <p class="mt-1 text-sm opacity-70">
            The team, its members, invitations and everything it owns are removed
            for good.
          </p>
          <button type="button" class="btn btn-error btn-sm mt-3" @click="askDelete">
            Delete team
          </button>
        </div>
      </section>

      <div v-else class="mt-6 text-sm opacity-70">
        Only the owner can change the team name, delete it, or change roles.
      </div>
      </template>
    </TeamTabs>

    <ConfirmDialog
      v-if="removing"
      heading="Remove this member?"
      :message="`${removing.name} will lose access to this team.`"
      confirm-label="Remove member"
      :busy="removeBusy"
      :error="removeError"
      @cancel="cancelRemove"
      @confirm="removeMember"
    />

    <ConfirmDialog
      v-if="deleting"
      heading="Delete this team?"
      message="The team, its members, invitations and everything it owns are removed for good."
      confirm-label="Delete team"
      :busy="deleteBusy"
      :error="deleteError"
      @cancel="deleting = false"
      @confirm="deleteTeam"
    />
  </div>
</template>
