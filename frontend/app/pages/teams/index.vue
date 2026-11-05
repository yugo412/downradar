<script setup lang="ts">
import type { Team, TeamInvitationView } from '~/types/api'

definePageMeta({ middleware: 'authenticated', ssr: false })

useSeoMeta({ title: 'Teams', robots: 'noindex, nofollow' })

const teams = ref<Team[]>([])
const invitations = ref<TeamInvitationView[]>([])
const limit = ref<number | null>(null)
const loading = ref(true)
const error = ref('')
const name = ref('')
const creating = ref(false)
const createError = ref('')
const accepting = ref<number | null>(null)
const acceptError = ref('')

const roleLabel: Record<string, string> = {
  owner: 'Owner',
  manager: 'Manager',
  member: 'Member',
}

const atLimit = computed(() => limit.value !== null && teams.value.length >= limit.value)

async function load() {
  try {
    const res = await $fetch<{
      teams: Team[]
      limit: number
      invitations: TeamInvitationView[]
    }>('/api/v1/teams')
    teams.value = res.teams ?? []
    limit.value = res.limit ?? null
    invitations.value = res.invitations ?? []
  } catch (err) {
    error.value = apiError(err, 'Could not load your teams.')
  } finally {
    loading.value = false
  }
}

async function accept(invitation: TeamInvitationView) {
  accepting.value = invitation.id
  acceptError.value = ''
  try {
    const res = await $fetch<{ team: Team }>('/api/v1/teams/invitations/accept', {
      method: 'POST',
      body: { id: invitation.id },
    })
    await navigateTo(`/teams/${res.team.slug}`)
  } catch (err) {
    acceptError.value = apiError(err, 'Could not accept the invitation.')
  } finally {
    accepting.value = null
  }
}

async function create() {
  creating.value = true
  createError.value = ''
  try {
    const res = await $fetch<{ team: Team }>('/api/v1/teams', {
      method: 'POST',
      body: { name: name.value.trim() },
    })
    await navigateTo(`/teams/${res.team.slug}`)
  } catch (err) {
    createError.value = apiError(err, 'Could not create the team.')
  } finally {
    creating.value = false
  }
}

onMounted(load)
</script>

<template>
  <div class="mx-auto max-w-6xl px-4 py-8">
    <header>
      <h1 class="text-2xl font-bold tracking-tight">Teams</h1>
      <p class="mt-1 max-w-2xl text-sm opacity-70">
        A team is a workspace beside your own. It has its own members and roles,
        and later its own services and alerts.
      </p>
    </header>

    <TeamTabs :teams="teams" :current="null" class="mt-6">
      <p v-if="error" role="alert" class="alert alert-error mb-4">{{ error }}</p>

    <section
      v-if="invitations.length"
      class="mb-6 rounded-box border border-primary/40 bg-base-100 p-5 shadow-sm"
    >
      <h2 class="text-lg font-semibold">Pending invitations</h2>
      <ul class="mt-3 divide-y divide-base-300">
        <li
          v-for="invitation in invitations"
          :key="invitation.id"
          class="flex flex-wrap items-center justify-between gap-3 py-3"
        >
          <div class="min-w-0">
            <p class="font-medium">{{ invitation.teamName }}</p>
            <p class="text-xs opacity-60">
              {{ roleLabel[invitation.role] }} ·
              invited by {{ invitation.invitedByName || 'a team owner' }} ·
              expires <LocalTime :at="invitation.expiresAt" />
            </p>
          </div>
          <button
            type="button"
            class="btn btn-primary btn-sm shrink-0"
            :disabled="accepting === invitation.id"
            @click="accept(invitation)"
          >
            <span v-if="accepting === invitation.id" class="loading loading-spinner loading-xs" />
            Accept
          </button>
        </li>
      </ul>
      <p v-if="acceptError" role="alert" class="mt-3 text-sm text-error">{{ acceptError }}</p>
    </section>

    <div>
      <h2 class="text-lg font-semibold">Personal</h2>
      <p class="mt-1 text-sm opacity-70">
        Your own services and alerts, which are not shared with a team.
      </p>
      <div class="mt-4 flex flex-wrap gap-2">
        <NuxtLink to="/services" class="btn btn-sm">Your services</NuxtLink>
        <NuxtLink to="/alerts" class="btn btn-sm">Your alerts</NuxtLink>
        <NuxtLink to="/account/profile" class="btn btn-ghost btn-sm">Edit profile</NuxtLink>
      </div>
    </div>

    <section class="mt-6">
      <div class="flex flex-wrap items-baseline justify-between gap-2">
        <h2 class="text-lg font-semibold">Your teams</h2>
        <span v-if="limit !== null" class="text-xs opacity-60">
          {{ teams.length }} of {{ limit }} teams
        </span>
      </div>

      <div v-if="loading" class="mt-4 flex items-center gap-2 text-sm opacity-70">
        <span class="loading loading-spinner loading-sm" />
        Loading your teams…
      </div>

      <p v-else-if="!teams.length" class="mt-3 text-sm opacity-70">
        You are not in a team yet. Create one below, or ask a team owner to invite
        you.
      </p>

      <ul v-else class="mt-4 grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
        <li v-for="team in teams" :key="team.id">
          <NuxtLink
            :to="`/teams/${team.slug}`"
            class="block h-full rounded-box border border-base-300 p-4 hover:border-primary"
          >
            <div class="flex flex-wrap items-center justify-between gap-2">
              <span class="font-medium">{{ team.name }}</span>
              <span class="badge badge-ghost badge-sm">{{ roleLabel[team.role] }}</span>
            </div>
            <p class="mt-2 text-sm opacity-70">Members, roles and invitations.</p>
          </NuxtLink>
        </li>
      </ul>
    </section>

    <section class="mt-6 border-t border-base-300 pt-6">
      <h2 class="text-lg font-semibold">Create a team</h2>
      <p class="mt-1 text-sm opacity-70">
        You become its owner. You can invite people once it exists.
      </p>

      <p v-if="atLimit" class="mt-4 text-sm opacity-70">
        Your account is already in {{ limit }} teams, which is the limit. Delete a
        team you own, or ask its owner to remove you, before creating another.
      </p>

      <form v-else class="join mt-4 w-full" @submit.prevent="create">
        <input
          v-model="name"
          type="text"
          class="input input-bordered join-item w-full"
          placeholder="Team name"
          aria-label="Team name"
          maxlength="80"
          required
        />
        <button type="submit" class="btn btn-primary join-item" :disabled="creating">
          <span v-if="creating" class="loading loading-spinner loading-xs" />
          Create team
        </button>
      </form>
      <p v-if="createError" role="alert" class="mt-3 text-sm text-error">{{ createError }}</p>
    </section>
    </TeamTabs>
  </div>
</template>
