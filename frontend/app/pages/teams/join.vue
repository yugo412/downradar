<script setup lang="ts">
import type { Team, TeamInvitationView } from '~/types/api'

// The link is in an email and the visitor may not have a session yet, so the
// page itself is public. Accepting it still needs the account the invitation
// names.
definePageMeta({ ssr: false })

useSeoMeta({ title: 'Join a team', robots: 'noindex, nofollow' })

const route = useRoute()
const { user, loaded, signOut } = useAuth()

const token = computed(() => String(route.query.token ?? ''))
const invitation = ref<TeamInvitationView | null>(null)
const loading = ref(true)
const error = ref('')
const joining = ref(false)
const joinError = ref('')
const joined = ref<Team | null>(null)

const roleLabel: Record<string, string> = {
  owner: 'owner',
  manager: 'manager',
  member: 'member',
}

const next = computed(
  () => `/teams/join?token=${encodeURIComponent(token.value)}`,
)
const sameAddress = computed(
  () =>
    !!invitation.value &&
    !!user.value &&
    invitation.value.email.toLowerCase() === user.value.email.toLowerCase(),
)

async function load() {
  if (!token.value) {
    error.value = 'This invitation link is incomplete.'
    loading.value = false
    return
  }
  try {
    const res = await $fetch<{ invitation: TeamInvitationView }>(
      `/api/v1/teams/invitations/${encodeURIComponent(token.value)}`,
    )
    invitation.value = res.invitation
  } catch (err) {
    error.value = apiError(err, 'This invitation is no longer available.')
  } finally {
    loading.value = false
  }
}

async function join() {
  joining.value = true
  joinError.value = ''
  try {
    const res = await $fetch<{ team: Team }>(
      `/api/v1/teams/invitations/${encodeURIComponent(token.value)}/accept`,
      { method: 'POST' },
    )
    joined.value = res.team
    await navigateTo(`/teams/${res.team.slug}`)
  } catch (err) {
    joinError.value = apiError(err, 'Could not join the team.')
  } finally {
    joining.value = false
  }
}

async function switchAccount() {
  await signOut()
  await navigateTo(`/login?next=${encodeURIComponent(next.value)}`)
}

onMounted(load)
</script>

<template>
  <div class="mx-auto max-w-xl px-4 py-12">
    <div class="rounded-box border border-base-300 bg-base-100 p-6 shadow-sm">
      <h1 class="text-2xl font-bold tracking-tight">Join a team</h1>

      <div v-if="loading" class="mt-4 flex items-center gap-2 text-sm opacity-70">
        <span class="loading loading-spinner loading-sm" />
        Reading the invitation…
      </div>

      <p v-else-if="error" role="alert" class="mt-4 text-sm text-error">{{ error }}</p>

      <template v-else-if="invitation">
        <p class="mt-3 text-sm opacity-80">
          <template v-if="invitation.invitedByName">
            {{ invitation.invitedByName }} invited you to join
          </template>
          <template v-else>You were invited to join</template>
          <strong>{{ invitation.teamName }}</strong> as a
          {{ roleLabel[invitation.role] ?? invitation.role }}.
        </p>

        <div v-if="!loaded" class="mt-5 flex items-center gap-2 text-sm opacity-70">
          <span class="loading loading-spinner loading-xs" />
          Checking your session…
        </div>

        <div v-else-if="!user" class="mt-5">
          <p class="text-sm opacity-70">
            Sign in as <strong>{{ invitation.email }}</strong> to accept it.
          </p>
          <NuxtLink
            :to="`/login?next=${encodeURIComponent(next)}`"
            class="btn btn-primary btn-sm mt-3"
          >
            Sign in to join
          </NuxtLink>
        </div>

        <div v-else-if="!sameAddress" class="mt-5">
          <p class="text-sm opacity-80">
            This invitation is for <strong>{{ invitation.email }}</strong>, but you
            are signed in as {{ user.email }}.
          </p>
          <button type="button" class="btn btn-sm mt-3" @click="switchAccount">
            Use another account
          </button>
        </div>

        <div v-else class="mt-5">
          <p class="text-sm opacity-70">
            This link works once, for the address it was sent to.
          </p>
          <button
            type="button"
            class="btn btn-primary btn-sm mt-3"
            :disabled="joining"
            @click="join"
          >
            <span v-if="joining" class="loading loading-spinner loading-xs" />
            Join {{ invitation.teamName }}
          </button>
          <p v-if="joinError" role="alert" class="mt-3 text-sm text-error">{{ joinError }}</p>
          <p v-if="joined" class="mt-3 text-sm text-success">
            You joined {{ joined.name }}.
          </p>
        </div>
      </template>
    </div>
  </div>
</template>
