<script setup lang="ts">
import type { Team, TeamInvitation, TeamMember } from '~/types/api'

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

// A team cannot be left without an owner, so an owner has to hand each team on
// or delete it before the account can go. The members and pending invitations
// are read per team so the choices can be offered here.
const teams = ref<Team[]>([])
const members = ref<Record<string, TeamMember[]>>({})
const invitations = ref<Record<string, TeamInvitation[]>>({})
const loadingTeams = ref(true)
const teamsError = ref('')
const ownedTeams = computed(() => teams.value.filter((team) => team.role === 'owner'))

const transferMember = reactive<Record<string, string>>({})
const transferEmail = reactive<Record<string, string>>({})
const transferBusy = ref('')
const transferError = ref('')
const transferNotice = ref('')
const deletingTeam = ref<Team | null>(null)

onMounted(loadTeams)

async function loadTeams() {
  teamsError.value = ''
  try {
    const res = await $fetch<{ teams: Team[] }>('/api/v1/teams')
    teams.value = res.teams ?? []
    await Promise.all(
      ownedTeams.value.map(async (team) => {
        const detail = await $fetch<{
          members: TeamMember[]
          invitations: TeamInvitation[]
        }>(`/api/v1/teams/${encodeURIComponent(team.slug)}`)
        members.value[team.slug] = detail.members ?? []
        invitations.value[team.slug] = detail.invitations ?? []
      }),
    )
  } catch (err) {
    teamsError.value = apiError(err, 'Could not load the teams you own.')
  } finally {
    loadingTeams.value = false
  }
}

// The people who can take the team over: everyone but the owner leaving.
function candidates(team: Team) {
  return (members.value[team.slug] ?? []).filter((member) => member.role !== 'owner')
}

// The live owner invitation, if one was sent to a new address. It is what the
// account deletion waits for.
function transferInvite(team: Team) {
  return (invitations.value[team.slug] ?? []).find((invitation) => invitation.transfer)
}

async function transferToMember(team: Team) {
  const target = transferMember[team.slug]
  if (!target) {
    transferError.value = 'Choose a member to take the team.'
    return
  }

  transferBusy.value = team.slug
  transferError.value = ''
  transferNotice.value = ''
  try {
    await $fetch(`/api/v1/teams/${encodeURIComponent(team.slug)}/transfer`, {
      method: 'POST',
      body: { userId: Number(target) },
    })
    transferNotice.value = `${team.name} now belongs to its new owner.`
    await loadTeams()
  } catch (err) {
    transferError.value = apiError(err, 'Could not transfer the team.')
  } finally {
    transferBusy.value = ''
  }
}

async function transferToEmail(team: Team) {
  const email = (transferEmail[team.slug] ?? '').trim()
  if (!email) {
    transferError.value = 'Enter an email address to invite.'
    return
  }

  transferBusy.value = team.slug
  transferError.value = ''
  transferNotice.value = ''
  try {
    const res = await $fetch<{ sent: boolean }>(
      `/api/v1/teams/${encodeURIComponent(team.slug)}/transfer`,
      { method: 'POST', body: { email } },
    )
    transferEmail[team.slug] = ''
    transferNotice.value = res.sent
      ? `Invitation sent to ${email}. The team stays yours until they accept it.`
      : `The invitation for ${email} was saved, but the email could not be sent.`
    await loadTeams()
  } catch (err) {
    transferError.value = apiError(err, 'Could not invite a new owner.')
  } finally {
    transferBusy.value = ''
  }
}

async function resendTransfer(team: Team, invitation?: TeamInvitation) {
  if (!invitation) return
  transferBusy.value = team.slug
  transferError.value = ''
  transferNotice.value = ''
  try {
    const res = await $fetch<{ sent: boolean }>(
      `/api/v1/teams/${encodeURIComponent(team.slug)}/invitations/${invitation.id}/resend`,
      { method: 'POST' },
    )
    transferNotice.value = res.sent
      ? `A fresh invitation went to ${invitation.email}.`
      : `The invitation for ${invitation.email} was replaced, but the email could not be sent.`
    await loadTeams()
  } catch (err) {
    transferError.value = apiError(err, 'Could not resend the invitation.')
  } finally {
    transferBusy.value = ''
  }
}

async function revokeTransfer(team: Team, invitation?: TeamInvitation) {
  if (!invitation) return
  transferBusy.value = team.slug
  transferError.value = ''
  transferNotice.value = ''
  try {
    await $fetch(
      `/api/v1/teams/${encodeURIComponent(team.slug)}/invitations/${invitation.id}`,
      { method: 'DELETE' },
    )
    transferNotice.value = `The invitation for ${invitation.email} was revoked.`
    await loadTeams()
  } catch (err) {
    transferError.value = apiError(err, 'Could not revoke the invitation.')
  } finally {
    transferBusy.value = ''
  }
}

function askDeleteTeam(team: Team) {
  removeError.value = ''
  deletingTeam.value = team
}

async function deleteOwnedTeam() {
  const team = deletingTeam.value
  if (!team) return

  removeBusy.value = true
  removeError.value = ''
  try {
    await $fetch(`/api/v1/teams/${encodeURIComponent(team.slug)}`, { method: 'DELETE' })
    deletingTeam.value = null
    transferNotice.value = `${team.name} and everything it owns are gone.`
    await loadTeams()
  } catch (err) {
    removeError.value = apiError(err, 'Could not delete the team.')
  } finally {
    removeBusy.value = false
  }
}

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
    // A team may have been added in another tab since this page opened. The
    // refusal names it, so read the teams again and show the choices.
    await loadTeams()
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

    <div
      v-if="loadingTeams"
      class="mt-8 rounded-box border border-base-300 bg-base-100 p-5 text-sm opacity-70 shadow-sm"
    >
      Checking the teams you own…
    </div>

    <div
      v-else-if="ownedTeams.length"
      class="mt-8 rounded-box border border-warning/40 bg-base-100 p-5 shadow-sm"
    >
      <h2 class="text-lg font-semibold">Teams you own</h2>
      <p class="mt-2 text-sm opacity-80">
        A team needs an owner. Hand each team below to somebody else, or delete it
        with everything it holds, before deleting your account.
      </p>
      <p v-if="teamsError" role="alert" class="mt-3 text-sm text-error">{{ teamsError }}</p>

      <div
        v-for="team in ownedTeams"
        :key="team.id"
        class="mt-4 rounded-box border border-base-300 p-4"
      >
        <div class="flex flex-wrap items-center justify-between gap-2">
          <NuxtLink :to="`/teams/${team.slug}`" class="link font-medium">
            {{ team.name }}
          </NuxtLink>
          <button
            type="button"
            class="btn btn-ghost btn-xs text-error"
            :disabled="removeBusy"
            @click="askDeleteTeam(team)"
          >
            Delete team
          </button>
        </div>

        <div
          v-if="transferInvite(team)"
          class="mt-3 rounded-box border border-base-300 bg-base-200 p-3 text-sm"
        >
          <p>
            Waiting for <strong>{{ transferInvite(team)?.email }}</strong> to accept
            ownership. The account cannot be deleted until they do.
          </p>
          <p class="mt-1 text-xs opacity-60">
            The link expires in seven days; sending it again replaces the old one.
          </p>
          <div class="mt-2 flex flex-wrap gap-1">
            <button
              type="button"
              class="btn btn-ghost btn-xs"
              :disabled="transferBusy === team.slug"
              @click="resendTransfer(team, transferInvite(team))"
            >
              Resend
            </button>
            <button
              type="button"
              class="btn btn-ghost btn-xs text-error"
              :disabled="transferBusy === team.slug"
              @click="revokeTransfer(team, transferInvite(team))"
            >
              Revoke
            </button>
          </div>
        </div>

        <template v-else>
          <form class="mt-3 flex flex-col gap-2 sm:flex-row" @submit.prevent="transferToMember(team)">
            <select
              v-model="transferMember[team.slug]"
              class="select select-bordered w-full"
              :aria-label="`Member to take over ${team.name}`"
            >
              <option value="">Choose a member…</option>
              <option
                v-for="member in candidates(team)"
                :key="member.userId"
                :value="String(member.userId)"
              >
                {{ member.name }} ({{ member.email }})
              </option>
            </select>
            <button
              type="submit"
              class="btn btn-sm shrink-0"
              :disabled="transferBusy === team.slug || !candidates(team).length"
            >
              Transfer
            </button>
          </form>
          <p v-if="!candidates(team).length" class="mt-1 text-xs opacity-60">
            The team has no other member yet, so invite a new owner instead.
          </p>

          <form class="mt-2 flex flex-col gap-2 sm:flex-row" @submit.prevent="transferToEmail(team)">
            <input
              v-model="transferEmail[team.slug]"
              type="email"
              class="input input-bordered w-full"
              placeholder="new-owner@example.com"
              :aria-label="`New owner email for ${team.name}`"
            />
            <button
              type="submit"
              class="btn btn-sm shrink-0"
              :disabled="transferBusy === team.slug"
            >
              Invite owner
            </button>
          </form>
        </template>
      </div>

      <p v-if="transferError" role="alert" class="mt-3 text-sm text-error">
        {{ transferError }}
      </p>
      <p v-if="transferNotice" role="status" class="mt-3 text-sm text-success">
        {{ transferNotice }}
      </p>
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

      <p v-else-if="loadingTeams" class="mt-3 text-sm opacity-70">
        Checking the teams you own…
      </p>

      <p v-else-if="ownedTeams.length" class="mt-3 text-sm opacity-70">
        Transfer or delete the teams above first. The account stays until every
        team it owns has another owner.
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

    <ConfirmDialog
      v-if="deletingTeam"
      heading="Delete this team?"
      :message="`${deletingTeam.name}, its members, invitations and everything it owns are removed for good.`"
      confirm-label="Delete team"
      :busy="removeBusy"
      :error="removeError"
      @cancel="deletingTeam = null"
      @confirm="deleteOwnedTeam"
    />
  </div>
</template>
