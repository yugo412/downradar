<script setup lang="ts">
import type { AlertSubscription } from '~/types/api'

// The link in a message opens this page instead of stopping or pausing the way by
// itself: mail clients and scanners fetch the links they are sent, and a link that
// acted on a fetch would quieten an alert nobody asked to quieten.
//
// It is rendered in the browser only, so the answer for one token is never
// cached and handed to another visitor.
definePageMeta({ ssr: false })

useHead({
  title: 'Alert messages',
  meta: [{ name: 'robots', content: 'noindex' }],
})

const route = useRoute()
const token = computed(() => String(route.query.token ?? ''))

const { data, error } = await useFetch<AlertSubscription>(
  () => `/api/v1/alerts/${token.value}`,
  { immediate: !!token.value },
)

// An email address is personal, so its token is enough. A chat, a channel or a
// webhook is read by everybody in it, so the API only answers the account that
// set it up; a visitor who is not signed in is sent to sign in and brought back
// here, where the session proves the same thing.
const errorStatus = computed(
  () => Number((error.value as { statusCode?: number } | null)?.statusCode ?? 0),
)
const notOwner = computed(() => errorStatus.value === 403)
const signingIn = computed(() => errorStatus.value === 401)

watch(
  errorStatus,
  (status) => {
    if (status !== 401) return
    navigateTo(`/login?next=${encodeURIComponent(route.fullPath)}`, { replace: true })
  },
  { immediate: true },
)

// A pause or a stop can also come back refused if the session ended while the
// page was open, so it takes the same way to sign in rather than printing a
// message over a page that no longer has a session.
function linkProblem(err: unknown) {
  if (Number((err as { statusCode?: number } | null)?.statusCode ?? 0) === 401) {
    navigateTo(`/login?next=${encodeURIComponent(route.fullPath)}`, { replace: true })
    return
  }
  problem.value = apiError(err)
}

// What the link stops, in the words the way it came by uses: an address gets
// emails, a chat, a Slack channel, a Discord channel, a Teams channel or a Lark
// group gets messages, an ntfy topic gets notifications, and the page says which
// so somebody opening a forwarded link knows what they are about to stop.
const channel = computed(() => {
  if (data.value?.kind === 'telegram') {
    return { title: 'Telegram alerts', what: 'messages', where: 'a chat' }
  }
  if (data.value?.kind === 'slack') {
    return { title: 'Slack alerts', what: 'messages', where: 'your Slack channel' }
  }
  if (data.value?.kind === 'discord') {
    return { title: 'Discord alerts', what: 'messages', where: 'your Discord channel' }
  }
  if (data.value?.kind === 'teams') {
    return { title: 'Teams alerts', what: 'messages', where: 'your Teams channel' }
  }
  if (data.value?.kind === 'lark') {
    return { title: 'Lark alerts', what: 'messages', where: 'your Lark group' }
  }
  if (data.value?.kind === 'ntfy') {
    return { title: 'ntfy alerts', what: 'notifications', where: 'your ntfy topic' }
  }
  return { title: 'Email alerts', what: 'emails', where: data.value?.destination ?? 'an address' }
})

const busy = ref(false)
const problem = ref('')
const stopped = ref(false)
// The way being quiet, which this page can change as well as showing: the link in
// a message is the whole of what proves it belongs to whoever opened it.
const paused = ref(false)

watch(data, (value) => {
  if (value) paused.value = value.paused === true
})

async function setPaused(next: boolean) {
  busy.value = true
  problem.value = ''

  try {
    await $fetch(`/api/v1/alerts/${token.value}`, {
      method: 'PATCH',
      body: { paused: next },
    })
    paused.value = next
  } catch (err) {
    linkProblem(err)
  } finally {
    busy.value = false
  }
}

async function unsubscribe() {
  busy.value = true
  problem.value = ''

  try {
    await $fetch(`/api/v1/alerts/${token.value}`, { method: 'DELETE' })
    stopped.value = true
  } catch (err) {
    linkProblem(err)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="mx-auto max-w-xl px-4 py-16">
    <NuxtLink to="/" class="link link-hover text-sm opacity-70">&larr; All services</NuxtLink>

    <div class="mt-4 rounded-box border border-base-300 bg-base-100 p-6 shadow-sm">
      <h1 class="text-xl font-semibold">{{ data ? channel.title : 'Alert messages' }}</h1>

      <p v-if="!token" class="mt-3 text-sm opacity-80">
        This link has no token in it. Open the unsubscribe link at the bottom of one of the
        messages again.
      </p>

      <p v-else-if="signingIn" class="mt-3 flex items-center gap-2 text-sm opacity-80">
        <span class="loading loading-spinner loading-xs" />
        Taking you to sign in&hellip;
      </p>

      <template v-else-if="notOwner">
        <p class="mt-3 text-sm opacity-80">
          This alert belongs to another account. Only the account that set it up can pause
          or stop it.
        </p>
        <div class="mt-5 flex flex-wrap items-center gap-2">
          <NuxtLink to="/alerts" class="btn btn-outline btn-sm">Go to your alerts</NuxtLink>
        </div>
      </template>

      <p v-else-if="error" class="mt-3 text-sm opacity-80">
        This link is not valid any more. If you still get them, use the unsubscribe link in
        the last message you received.
      </p>

      <template v-else-if="data">
        <p v-if="stopped || data.unsubscribedAt" class="mt-3 text-sm">
          You will not get any more {{ channel.what }} about {{ data.vendorName }}.
        </p>

        <template v-else>
          <p class="mt-3 text-sm opacity-80">
            <template v-if="paused">
              The {{ channel.what }} about <strong>{{ data.vendorName }}</strong> are
              paused. Nothing is going out, and nothing has been taken away.
            </template>
            <template v-else>
              The {{ channel.what }} about <strong>{{ data.vendorName }}</strong> go to
              {{ channel.where }}. They can be paused for a while, or stopped for good.
            </template>
          </p>
          <p v-if="problem" role="alert" class="mt-3 text-sm text-error">{{ problem }}</p>

          <div class="mt-5 flex flex-wrap items-center gap-2">
            <button
              type="button"
              class="btn btn-outline btn-sm"
              :disabled="busy"
              @click="setPaused(!paused)"
            >
              <span v-if="busy" class="loading loading-spinner loading-xs" />
              {{ paused ? `Start the ${channel.what} again` : `Pause the ${channel.what}` }}
            </button>
            <button type="button" class="btn btn-error btn-sm" :disabled="busy" @click="unsubscribe">
              Stop them for good
            </button>
            <NuxtLink :to="`/${data.vendorSlug}`" class="btn btn-ghost btn-sm">
              Keep the alerts
            </NuxtLink>
          </div>
        </template>
      </template>

      <div v-else class="mt-3 flex items-center gap-3 text-sm opacity-70">
        <span class="loading loading-spinner loading-xs" />
        Loading&hellip;
      </div>
    </div>
  </div>
</template>
