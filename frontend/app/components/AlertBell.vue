<script setup lang="ts">
import type { AlertState, ComponentStatus } from '~/types/api'

const props = defineProps<{
  slug: string
  name: string
  status: string
  components: ComponentStatus[]
}>()

const { user } = useAuth()

// What the account already has here, which is all the bell needs to say what the
// alert is doing. A visitor who is signed out never asks: an alert made without an
// account belongs to the address they typed, and nothing on the page can say
// whether it is theirs.
const subscribed = ref(false)
const paused = ref(false)

// What this alert watches, for the dialog a visitor without an account gets, where
// the whole service is the only thing that can be picked.
const whole = ref(true)
const picked = ref<string[]>([])

const open = ref(false)
const email = ref('')
const busy = ref(false)
const error = ref('')
const step = ref<'form' | 'subscribed'>('form')

// Which of the two ways the dialog is showing. A visitor without an account can
// be told by an address they type, or can follow a feed in a reader of their own,
// which needs nothing from here at all.
const tab = ref<'email' | 'rss'>('email')

const nothingPicked = computed(() => !whole.value && picked.value.length === 0)

watch(user, () => void check(), { immediate: true })

async function check() {
  subscribed.value = false
  paused.value = false
  if (!user.value) return

  try {
    const state = await $fetch<AlertState>(`/api/v1/vendors/${props.slug}/alerts`)
    subscribed.value = state.subscribed
    paused.value = state.paused ?? false
  } catch {
    // Not worth an error on a public page: the bell simply goes on offering an
    // alert, and the page behind it is where anything that goes wrong is said.
  }
}

// The button says what the alert is doing rather than only that one exists. A
// paused alert is still an alert, but nothing is going out, and the bell is the
// one thing on the page that says so before the dialog is opened.
const bellLabel = computed(() => {
  if (paused.value) return 'Paused'
  return subscribed.value ? 'Subscribed' : 'Get alerts'
})

const bellClass = computed(() => {
  if (paused.value) return 'opacity-40'
  return subscribed.value ? 'text-success' : ''
})

function start() {
  open.value = true
  error.value = ''
  step.value = 'form'
  tab.value = 'email'
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
    // does not change what the bell says.
    step.value = 'subscribed'
  } catch (err) {
    error.value = apiError(err)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <!-- An account keeps its alert on a page of its own, where both the ways it can
       arrive are set up and the components can be picked. A visitor without one
       gets the dialog, because the address they type is the whole of their
       alert. -->
  <!-- Which one this is depends on who is asking, and this page is cached and
       shared, so the server never knows. The button is what it renders, and the
       link takes its place once the browser has asked. -->
  <ClientOnly>
    <NuxtLink
      v-if="user"
      :to="`/alerts/${props.slug}`"
      class="btn btn-ghost btn-sm gap-2"
    >
      <BellIcon class="h-4 w-4" :class="bellClass" />
      {{ bellLabel }}
    </NuxtLink>

    <button v-else type="button" class="btn btn-ghost btn-sm gap-2" @click="start">
      <BellIcon class="h-4 w-4" :class="bellClass" />
      {{ bellLabel }}
    </button>

    <template #fallback>
      <button type="button" class="btn btn-ghost btn-sm gap-2">
        <BellIcon class="h-4 w-4" />
        Get alerts
      </button>
    </template>
  </ClientOnly>

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
        <h2 class="text-lg font-semibold">Alerts for {{ name }}</h2>
        <button type="button" class="btn btn-ghost btn-sm" aria-label="Close" @click="close">
          ✕
        </button>
      </div>

      <!-- The two things a visitor without an account can do here: leave an
           address, or follow a feed that needs nothing at all. -->
      <div role="tablist" class="tabs tabs-box mx-5 mt-4 w-fit">
        <button
          type="button"
          role="tab"
          class="tab"
          :class="{ 'tab-active': tab === 'email' }"
          @click="tab = 'email'"
        >
          Email
        </button>
        <button
          type="button"
          role="tab"
          class="tab"
          :class="{ 'tab-active': tab === 'rss' }"
          @click="tab = 'rss'"
        >
          RSS
        </button>
      </div>

      <div v-if="tab === 'rss'" class="min-h-0 flex-1 overflow-y-auto px-5 py-4">
        <AlertFeed :slug="slug" :name="name" />
      </div>

      <div v-else-if="step === 'subscribed'" class="px-5 py-4">
        <p class="text-sm">
          {{ email.trim() }} will get an email each time {{ name }} changes, in either
          direction.
        </p>
        <p class="mt-2 text-sm opacity-70">Every email has a link to stop them again.</p>
        <p class="mt-2 text-sm opacity-70">
          <NuxtLink to="/login" class="link">Sign in</NuxtLink>
          to keep the alert in one place and add a webhook, a chat, a Slack
          channel, a Discord channel, a Lark group or an ntfy topic as well.
        </p>
        <div class="mt-4 flex justify-end">
          <button type="button" class="btn btn-sm" @click="close">Close</button>
        </div>
      </div>

      <form v-else class="flex min-h-0 flex-1 flex-col" @submit.prevent="subscribe">
        <div class="px-5 pt-4">
          <p class="text-sm opacity-80">
            Get an email each time {{ name }} changes status, in either direction.
          </p>
          <input
            v-model="email"
            type="email"
            required
            autocomplete="email"
            placeholder="you@example.com"
            class="input input-bordered mt-3 w-full"
          >
        </div>

        <div class="mt-3 flex min-h-0 flex-1 flex-col">
          <AlertTargets
            v-model:whole="whole"
            v-model:picked="picked"
            :slug="slug"
            :name="name"
            :status="status"
            :components="components"
            guest
            fill
          />
        </div>

        <div class="border-t border-base-300 px-5 py-3">
          <p v-if="error" role="alert" class="mb-3 text-sm text-error">{{ error }}</p>
          <p v-if="nothingPicked" class="mb-3 text-sm opacity-70">
            Pick at least one thing to be emailed about.
          </p>
          <div class="flex flex-wrap items-center justify-end gap-2">
            <button type="button" class="btn btn-ghost btn-sm" :disabled="busy" @click="close">
              Cancel
            </button>
            <button type="submit" class="btn btn-primary btn-sm" :disabled="busy || nothingPicked">
              <span v-if="busy" class="loading loading-spinner loading-xs" />
              Save alert
            </button>
          </div>
        </div>
      </form>
    </div>
  </div>
</template>
