<script setup lang="ts">
import type { AlertState, ComponentStatus } from '~/types/api'

const props = defineProps<{
  slug: string
  name: string
  status: string
  components: ComponentStatus[]
}>()

const { user } = useAuth()

// What the account already has here, as the API reports it. A visitor who is
// signed out never asks: an alert made without an account belongs to the address
// they typed, and nothing on the page can say whether it is theirs.
const subscribed = ref(false)
const paused = ref(false)
const maskedEmail = ref('')

// What the alert watches: the vendor's own status, and the components it was
// told about by the identifiers the API takes back.
const whole = ref(true)
const picked = ref<string[]>([])

const open = ref(false)
const email = ref('')
const busy = ref(false)
const error = ref('')
const step = ref<'form' | 'subscribed' | 'stopped'>('form')

// A status page can report hundreds of components, so the list is searchable.
// The list is already on the page, so the filter runs in the browser: it is
// instant, and opening the modal costs no request of its own.
const search = ref('')
const needle = computed(() => search.value.trim().toLowerCase())
const matched = computed(() => {
  if (!needle.value) return props.components
  return props.components.filter(
    (c) =>
      c.name.toLowerCase().includes(needle.value) ||
      (c.group ?? '').toLowerCase().includes(needle.value),
  )
})

const nothingPicked = computed(() => !whole.value && picked.value.length === 0)

// What was asked for, said in one phrase, so the confirmation reads as a
// sentence about what will arrive rather than about a selection.
const watched = computed(() => {
  const names = picked.value.length
  if (names === 0) return props.name
  if (names === 1) {
    const one = props.components.find((c) => c.sourceId === picked.value[0])
    if (one) {
      return whole.value ? `${props.name} and ${one.name}` : `${one.name} on ${props.name}`
    }
  }
  return whole.value
    ? `${props.name} and ${names} of its components`
    : `${names} components of ${props.name}`
})

watch(user, () => void check(), { immediate: true })

async function check() {
  subscribed.value = false
  paused.value = false
  maskedEmail.value = ''
  whole.value = true
  picked.value = []
  if (!user.value) return

  try {
    const state = await $fetch<AlertState>(`/api/v1/vendors/${props.slug}/alerts`)
    subscribed.value = state.subscribed
    paused.value = state.paused ?? false
    maskedEmail.value = state.email ?? ''
    whole.value = state.wholeService ?? true
    picked.value = state.components ?? []
  } catch {
    // Not worth an error on a public page: the bell simply goes on offering an
    // alert, and the modal is where anything that goes wrong is said.
  }
}

function start() {
  open.value = true
  error.value = ''
  search.value = ''
  step.value = 'form'

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
      body: {
        email: email.value.trim(),
        wholeService: whole.value,
        components: picked.value,
      },
    })
    // Only an account can be read back later, so a signed out visitor's alert
    // does not change what the bell says. Saving is asking for the emails, so it
    // starts them again if they were paused.
    subscribed.value = !!user.value
    paused.value = false
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
    paused.value = false
    maskedEmail.value = ''
    whole.value = true
    picked.value = []
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
    <div
      class="flex max-h-[90vh] w-full max-w-2xl flex-col rounded-box border border-base-300 bg-base-100 shadow-xl"
    >
      <div class="flex items-center justify-between border-b border-base-300 px-5 py-3">
        <h2 class="text-lg font-semibold">Email alerts</h2>
        <button type="button" class="btn btn-ghost btn-sm" aria-label="Close" @click="close">
          ✕
        </button>
      </div>

      <div v-if="step === 'subscribed'" class="px-5 py-4">
        <p class="text-sm">
          {{ email.trim() }} will get an email each time {{ watched }} changes, in either
          direction.
        </p>
        <p class="mt-2 text-sm opacity-70">Every email has a link to stop them again.</p>
        <div class="mt-4 flex justify-end">
          <button type="button" class="btn btn-sm" @click="close">Close</button>
        </div>
      </div>

      <div v-else-if="step === 'stopped'" class="px-5 py-4">
        <p class="text-sm">You will not get any more emails about {{ name }}.</p>
        <p v-if="user" class="mt-2 text-sm opacity-70">
          You can start them again from
          <NuxtLink to="/alerts" class="link">your alerts page</NuxtLink>.
        </p>
        <div class="mt-4 flex justify-end">
          <button type="button" class="btn btn-sm" @click="close">Close</button>
        </div>
      </div>

      <form v-else class="flex min-h-0 flex-1 flex-col" @submit.prevent="subscribe">
        <div class="px-5 pt-4">
          <p class="text-sm opacity-80">
            Get an email each time {{ name }} changes status, in either direction, or tick
            the components you would rather hear about on their own.
          </p>
          <p v-if="subscribed" class="mt-2 text-sm opacity-80">
            These go to {{ maskedEmail }}.
            <template v-if="paused">They are paused at the moment, so saving starts them again.</template>
          </p>
          <input
            v-model="email"
            type="email"
            required
            autocomplete="email"
            placeholder="you@example.com"
            class="input input-bordered mt-3 w-full"
          >
          <input
            v-model="search"
            type="search"
            aria-label="Search components"
            placeholder="Search components"
            class="input input-bordered mt-3 w-full"
          >
        </div>

        <!-- The page can report hundreds of components, so the list scrolls and
             the footer stays where it is. -->
        <div class="mt-3 min-h-0 flex-1 overflow-y-auto border-t border-base-300">
          <label class="flex cursor-pointer items-center gap-3 px-5 py-2 hover:bg-base-200">
            <input v-model="whole" type="checkbox" class="checkbox checkbox-sm">
            <span class="flex-1 text-sm font-medium">The whole service</span>
            <StatusBadge :status="status" size="xs" />
          </label>

          <label
            v-for="component in matched"
            :key="component.sourceId"
            class="flex cursor-pointer items-center gap-3 px-5 py-2 hover:bg-base-200"
          >
            <input
              v-model="picked"
              type="checkbox"
              class="checkbox checkbox-sm"
              :value="component.sourceId"
            >
            <span class="min-w-0 flex-1 text-sm">
              {{ component.name }}
              <span v-if="component.group" class="opacity-60">· {{ component.group }}</span>
            </span>
            <StatusBadge :status="component.status" size="xs" />
          </label>

          <p v-if="!matched.length" class="px-5 py-4 text-sm opacity-70">
            No component matches that search.
          </p>
        </div>

        <div class="border-t border-base-300 px-5 py-3">
          <p v-if="error" role="alert" class="mb-3 text-sm text-error">{{ error }}</p>
          <p v-if="nothingPicked" class="mb-3 text-sm opacity-70">
            Pick at least one thing to be emailed about.
          </p>
          <div class="flex flex-wrap items-center justify-between gap-2">
            <button
              v-if="subscribed"
              type="button"
              class="btn btn-error btn-sm"
              :disabled="busy"
              @click="unsubscribe"
            >
              <span v-if="busy" class="loading loading-spinner loading-xs" />
              Unsubscribe
            </button>
            <span v-else />

            <div class="flex items-center gap-2">
              <button type="button" class="btn btn-ghost btn-sm" :disabled="busy" @click="close">
                Cancel
              </button>
              <button
                type="submit"
                class="btn btn-primary btn-sm"
                :disabled="busy || nothingPicked"
              >
                <span v-if="busy" class="loading loading-spinner loading-xs" />
                {{ subscribed ? 'Save changes' : 'Save alert' }}
              </button>
            </div>
          </div>
        </div>
      </form>
    </div>
  </div>
</template>
