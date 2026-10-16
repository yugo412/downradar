<script setup lang="ts">
import type { AlertState } from '~/types/api'

const props = defineProps<{
  slug: string
  name: string
}>()

const { user } = useAuth()

// What the account already has here, as the API reports it. A visitor who is
// signed out never asks: an alert made without an account belongs to the address
// they typed, and nothing on the page can say whether it is theirs.
const subscribed = ref(false)
const paused = ref(false)
const maskedEmail = ref('')

const open = ref(false)
const email = ref('')
const busy = ref(false)
const error = ref('')
const step = ref<'form' | 'already' | 'subscribed' | 'stopped'>('form')

watch(user, () => void check(), { immediate: true })

async function check() {
  subscribed.value = false
  paused.value = false
  maskedEmail.value = ''
  if (!user.value) return

  try {
    const state = await $fetch<AlertState>(`/api/v1/vendors/${props.slug}/alerts`)
    subscribed.value = state.subscribed
    paused.value = state.paused ?? false
    maskedEmail.value = state.email ?? ''
  } catch {
    // Not worth an error on a public page: the bell simply goes on offering an
    // alert, and the modal is where anything that goes wrong is said.
  }
}

function start() {
  open.value = true
  error.value = ''
  step.value = subscribed.value ? 'already' : 'form'

  // Whoever is signed in almost always wants their own address, so it is filled
  // in for them. Anything already typed is left alone.
  if (!email.value) email.value = user.value?.email ?? ''
}

function close() {
  open.value = false
}

async function subscribe() {
  busy.value = true
  error.value = ''

  try {
    await $fetch(`/api/v1/vendors/${props.slug}/alerts`, {
      method: 'POST',
      body: { email: email.value.trim() },
    })
    // Only an account can be read back later, so a signed out visitor's alert
    // does not change what the bell says.
    subscribed.value = !!user.value
    step.value = 'subscribed'
  } catch (err) {
    error.value = apiError(err)
  } finally {
    busy.value = false
  }
}

async function unsubscribe() {
  busy.value = true
  error.value = ''

  try {
    await $fetch(`/api/v1/vendors/${props.slug}/alerts`, { method: 'DELETE' })
    subscribed.value = false
    maskedEmail.value = ''
    step.value = 'stopped'
  } catch (err) {
    error.value = apiError(err)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <button type="button" class="btn btn-ghost btn-sm gap-2" @click="start">
    <svg
      xmlns="http://www.w3.org/2000/svg"
      class="h-4 w-4"
      :class="subscribed ? 'text-success' : ''"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      stroke-width="2"
      stroke-linecap="round"
      stroke-linejoin="round"
      aria-hidden="true"
    >
      <path d="M18 8a6 6 0 0 0-12 0c0 7-3 9-3 9h18s-3-2-3-9" />
      <path d="M13.7 21a2 2 0 0 1-3.4 0" />
    </svg>
    {{ subscribed ? 'Subscribed' : 'Get alerts' }}
  </button>

  <div
    v-if="open"
    class="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4"
    role="dialog"
    aria-modal="true"
    @click.self="close"
  >
    <div class="w-full max-w-md rounded-box border border-base-300 bg-base-100 shadow-xl">
      <div class="flex items-center justify-between border-b border-base-300 px-5 py-3">
        <h2 class="text-lg font-semibold">Email alerts</h2>
        <button type="button" class="btn btn-ghost btn-sm" aria-label="Close" @click="close">
          ✕
        </button>
      </div>

      <div v-if="step === 'subscribed'" class="px-5 py-4">
        <p class="text-sm">
          {{ email.trim() }} will get an email whenever {{ name }} changes status.
        </p>
        <p class="mt-2 text-sm opacity-70">Every email has a link to stop them again.</p>
        <div class="mt-4 flex justify-end">
          <button type="button" class="btn btn-sm" @click="close">Close</button>
        </div>
      </div>

      <div v-else-if="step === 'stopped'" class="px-5 py-4">
        <p class="text-sm">You will not get any more emails about {{ name }}.</p>
        <div class="mt-4 flex justify-end">
          <button type="button" class="btn btn-sm" @click="close">Close</button>
        </div>
      </div>

      <div v-else-if="step === 'already'" class="px-5 py-4">
        <p v-if="paused" class="text-sm">
          Your alerts for {{ name }} are paused, and they go to {{ maskedEmail }}. You can start
          them again from your alerts page.
        </p>
        <p v-else class="text-sm">
          You already get an email whenever {{ name }} changes status. It goes to {{ maskedEmail }}.
        </p>
        <p v-if="error" role="alert" class="mt-3 text-sm text-error">{{ error }}</p>
        <div class="mt-4 flex flex-wrap items-center justify-end gap-2">
          <NuxtLink v-if="paused" to="/alerts" class="btn btn-ghost btn-sm">
            Your alerts
          </NuxtLink>
          <button type="button" class="btn btn-ghost btn-sm" :disabled="busy" @click="close">
            Close
          </button>
          <button
            v-if="!paused"
            type="button"
            class="btn btn-error btn-sm"
            :disabled="busy"
            @click="unsubscribe"
          >
            <span v-if="busy" class="loading loading-spinner loading-xs" />
            Unsubscribe
          </button>
        </div>
      </div>

      <form v-else class="px-5 py-4" @submit.prevent="subscribe">
        <p class="text-sm opacity-80">
          Get an email each time {{ name }} changes status, in either direction. No account is
          needed.
        </p>
        <input
          v-model="email"
          type="email"
          required
          autocomplete="email"
          placeholder="you@example.com"
          class="input input-bordered mt-4 w-full"
        >
        <p v-if="error" role="alert" class="mt-3 text-sm text-error">{{ error }}</p>
        <div class="mt-4 flex items-center justify-end gap-2">
          <button type="button" class="btn btn-ghost btn-sm" :disabled="busy" @click="close">
            Cancel
          </button>
          <button type="submit" class="btn btn-primary btn-sm" :disabled="busy">
            <span v-if="busy" class="loading loading-spinner loading-xs" />
            Save alert
          </button>
        </div>
      </form>
    </div>
  </div>
</template>
