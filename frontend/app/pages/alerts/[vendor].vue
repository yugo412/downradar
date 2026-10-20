<script setup lang="ts">
import type { AlertState, AlertWebhook, VendorDetail } from '~/types/api'

definePageMeta({ middleware: 'authenticated', ssr: false })

useSeoMeta({ title: 'Alert', robots: 'noindex, nofollow' })

const { user } = useAuth()

const route = useRoute()
const slug = computed(() => String(route.params.vendor ?? ''))
const origin = useRequestURL().origin

const { data: vendor, error: vendorError } = await useFetch<VendorDetail>(
  () => `/api/v1/vendors/${slug.value}`,
)

if (vendorError.value?.statusCode === 404) {
  throw createError({ statusCode: 404, statusMessage: 'Service not found', fatal: true })
}

// The ways an alert can arrive, and a blank webhook the form starts from. The
// feed is the last one: it is not an alert the site keeps, so nothing about it
// is saved, but it is offered beside the others because that is where somebody
// looking for it will look.
type Way = 'email' | 'webhook' | 'telegram' | 'rss'

function blankWebhook(): AlertWebhook {
  return { url: '', method: 'POST', headers: [] }
}

const state = ref<AlertState | null>(null)
const loading = ref(true)
const busy = ref(false)
const saved = ref(false)
const error = ref('')

// Trying a webhook, and what came back from the one that was tried.
const testing = ref(false)
const testResult = ref<{ status: number | null; response: string; error: string } | null>(null)

// The sample body is long and is meant to be pasted somewhere else, so the page
// hands it over as it is shown. The button says so for a moment, then goes back
// to being pressable.
const copied = ref(false)
let copiedTimer: ReturnType<typeof setTimeout> | undefined

// Which way is being paused while the request is in flight.
const pausing = ref<Way | ''>('')

// What the form holds. The address is what the email way goes to, so an empty one
// is the way being off; the webhook is the same, with an empty URL, and the chat
// with an empty chat id. The bot is the subscriber's own, so its token is filled
// in when it is saved and masked until the eye in the field is pressed.
const email = ref('')
const hook = ref<AlertWebhook>(blankWebhook())
const chat = ref('')
const botToken = ref('')
const way = ref<Way>('email')

// Trying the chat, and what the chat service answered. It is a message like a
// delivery, so what comes back is whether it arrived rather than a body.
const trying = ref(false)
const testAnswer = ref<{ ok: boolean; error: string } | null>(null)

// What the email reports on. A call reports on the whole service, whatever is
// picked here, because its body says what the service looks like now.
const whole = ref(true)
const picked = ref<string[]>([])

const emailsOn = computed(() => email.value.trim() !== '')
const callsOn = computed(() => hook.value.url.trim() !== '')
const chatsOn = computed(() => chat.value.trim() !== '')
const anyWay = computed(() => emailsOn.value || callsOn.value || chatsOn.value)

// What is wrong with the webhook URL as it stands. An empty one is not wrong:
// that is how the calls are stopped. Anything else has to be an address the
// server could really call, and the page says so at the field rather than
// letting Save, or a test call, go out to something that is not an address.
const hookProblem = computed(() => {
  const url = hook.value.url.trim()
  if (url === '') return ''
  if (url.length > 1024) return 'That web address is too long.'

  let parsed: URL
  try {
    parsed = new URL(url)
  } catch {
    return 'That does not look like a web address. It needs http:// or https:// and a host.'
  }
  if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
    return 'A web address to call has to start with http:// or https://.'
  }
  return ''
})

// An address or a chat with nothing picked beside it would be a way of being told
// nothing, since both report on what was picked.
const nothingToTellAbout = computed(
  () => (emailsOn.value || chatsOn.value) && !whole.value && picked.value.length === 0,
)

// What the saved ways are, which is what a pause can be asked about: a way has
// to exist before it can be stopped, and what the fields hold and have not been
// saved yet is not one yet.
const savedEmail = computed(() => String(state.value?.email ?? ''))
const savedWebhook = computed(() => String(state.value?.webhook?.url ?? ''))
const savedTelegram = computed(() => String(state.value?.telegram ?? ''))
const emailPaused = computed(() => state.value?.emailPaused === true)
const webhookPaused = computed(() => state.value?.webhookPaused === true)
const telegramPaused = computed(() => state.value?.telegramPaused === true)

// Whether the site has a sender loop at all, which is what decides if a chat is
// offered.
const telegramEnabled = computed(() => state.value?.telegramEnabled === true)

// Nothing arrives when every way the alert has is quiet. That is what the
// account screen calls a paused alert, and it is the one state worth a banner
// here, since a subscriber who paused from that screen is stopping by this one.
const nothingArriving = computed(() => {
  const ways = [
    { set: savedEmail.value !== '', paused: emailPaused.value },
    { set: savedWebhook.value !== '', paused: webhookPaused.value },
    { set: savedTelegram.value !== '', paused: telegramPaused.value },
  ].filter((way) => way.set)
  return state.value?.subscribed === true && ways.length > 0 && ways.every((way) => way.paused)
})

// What the menu says each way is doing. It is read from the saved alert rather
// than from the fields, so a way that has been typed into and not saved yet is
// not claimed to be on by the menu beside the panel.
function wayState(on: boolean, paused: boolean) {
  if (!on) return { label: 'Off', cls: 'badge-ghost' }
  if (paused) return { label: 'Paused', cls: 'badge-warning' }
  return { label: 'On', cls: 'badge-success' }
}

const emailWay = computed(() => wayState(savedEmail.value !== '', emailPaused.value))
const webhookWay = computed(() => wayState(savedWebhook.value !== '', webhookPaused.value))
const telegramWay = computed(() => wayState(savedTelegram.value !== '', telegramPaused.value))

// The address on the account, offered as a one click fill in the email panel
// while the field is empty.
const accountEmail = computed(() => String(user.value?.email ?? ''))

// The calls this alert made lately, which is what the webhook panel shows under
// the switch: what it does, rather than only what it is set to.
const calls = computed(() => state.value?.calls ?? [])

function statusClass(status: number | null) {
  if (status === null) return 'badge-error'
  return status >= 200 && status < 300 ? 'badge-success' : 'badge-error'
}

const placeholderHint = [
  { token: '{{service}}', says: 'the service’s name' },
  { token: '{{slug}}', says: 'its address on this site' },
  { token: '{{component}}', says: 'the component that changed, empty for the whole service' },
  { token: '{{from}}', says: 'the state it left' },
  { token: '{{to}}', says: 'the state it is in now' },
  { token: '{{at}}', says: 'when it changed, as an RFC 3339 timestamp' },
  { token: '{{url}}', says: 'the service’s page here' },
  { token: '{{text}}', says: 'a sentence about the change, ready to send' },
]

// What a call carries, shown as the call would look for this service: its own
// name and components are real, and the part that only the call can know is an
// example. The statuses are the names this site stores them as.
const sample = computed(() => {
  const components = vendor.value?.componentsList ?? []
  return JSON.stringify(
    {
      service: {
        name: vendor.value?.name ?? '',
        slug: slug.value,
        url: `${origin}/${slug.value}`,
        status: vendor.value?.status ?? 'unknown',
      },
      changed: {
        component: components[0]?.name ?? '',
        from: 'ok',
        to: 'major',
        at: '2026-01-31T09:30:00Z',
        text: `${vendor.value?.name ?? 'The service'} is now Major outage`,
      },
      components: components.map((component) => ({
        name: component.name,
        group: component.group ?? '',
        status: component.status,
      })),
    },
    null,
    2,
  )
})

onMounted(load)

async function load() {
  try {
    const res = await $fetch<AlertState>(`/api/v1/vendors/${slug.value}/alerts`)
    state.value = res
    testResult.value = null

    // A way that is off shows as empty, which is also how it is turned off again.
    // The address on the account is offered as a one click fill instead of being
    // put in the field, since anything in the field is saved with the alert: a
    // webhook alone used to bring the address along with it.
    email.value = res.subscribed ? res.email ?? '' : ''
    hook.value = res.webhook
      ? { ...blankWebhook(), ...res.webhook }
      : blankWebhook()
    chat.value = res.subscribed ? res.telegram ?? '' : ''
    botToken.value = res.subscribed ? res.telegramBot ?? '' : ''
    testAnswer.value = null

    whole.value = res.subscribed ? res.wholeService ?? true : true
    picked.value = res.subscribed ? res.components ?? [] : []

    // The way that is already set up decides which panel opens.
    if (res.subscribed && !res.email && res.webhook && !res.telegram) way.value = 'webhook'
    else if (res.subscribed && !res.email && !res.webhook && res.telegram) way.value = 'telegram'
  } catch (err) {
    error.value = apiError(err, 'Could not load this alert.')
  } finally {
    loading.value = false
  }
}

async function save() {
  if (hookProblem.value) return

  busy.value = true
  error.value = ''
  saved.value = false

  try {
    await $fetch(`/api/v1/vendors/${slug.value}/alerts`, {
      method: 'POST',
      body: {
        email: email.value.trim(),
        // Both ways are sent every time, so what is off stays off. An empty URL
        // is a webhook being stopped rather than one being left as it was.
        webhook: callsOn.value
          ? {
              url: hook.value.url.trim(),
              method: hook.value.method,
              headers: hook.value.headers,
            }
          : { url: '' },
        // A chat is sent every time as well, so the panel says where the messages
        // go: an empty one is the way being stopped rather than left as it was.
        telegram: { chatId: chat.value.trim(), botToken: botToken.value.trim() },
        wholeService: whole.value,
        components: picked.value,
      },
    })
    await load()
    saved.value = true
  } catch (err) {
    error.value = apiError(err)
  } finally {
    busy.value = false
  }
}

// The switch says the way is working, so it is on when nothing is paused.
// Pausing is a change of its own rather than part of the save, so it goes out as
// soon as the switch moves, and the switch is put back where it was if the
// request is refused. It is deliberately not a whole reload: what is typed in the
// fields and not saved yet stays where it is.
async function setPaused(way: Way, event: Event) {
  const current = state.value
  if (!current) return

  const box = event.target as HTMLInputElement
  const on = box.checked
  pausing.value = way
  error.value = ''

  try {
    await $fetch(`/api/v1/vendors/${slug.value}/alerts/pause`, {
      method: 'POST',
      body: { kind: way, paused: !on },
    })
    if (way === 'email') current.emailPaused = !on
    else if (way === 'webhook') current.webhookPaused = !on
    else current.telegramPaused = !on
  } catch (err) {
    error.value = apiError(err)
    box.checked = !on
  } finally {
    pausing.value = ''
  }
}

// A test call goes out with what the page is holding, saved or not, so a webhook
// can be checked before it is relied on. What comes back is what the far end
// said, which is shown as it arrived.
async function tryWebhook() {
  if (hookProblem.value) return

  testing.value = true
  testResult.value = null
  error.value = ''

  try {
    testResult.value = await $fetch<{ status: number | null; response: string; error: string }>(
      `/api/v1/vendors/${slug.value}/alerts/test`,
      {
        method: 'POST',
        body: {
          url: hook.value.url.trim(),
          method: hook.value.method,
          headers: hook.value.headers,
        },
      },
    )
    await refreshCalls()
  } catch (err) {
    // A refusal is shown in the answer box beside the button rather than in the
    // banner at the top of the page, which is off screen from down here.
    testResult.value = {
      status: null,
      response: '',
      error: apiError(err, 'The call could not be made.'),
    }
  } finally {
    testing.value = false
  }
}

// A message is sent to the chat the page is holding, saved or not, so a chat can
// be checked before it is relied on. What comes back is whether it arrived, and
// what the chat service said when it did not.
async function tryTelegram() {
  if (!chat.value.trim()) return

  trying.value = true
  testAnswer.value = null
  error.value = ''

  try {
    testAnswer.value = await $fetch<{ ok: boolean; error: string }>(
      `/api/v1/vendors/${slug.value}/alerts/telegram/test`,
      {
        method: 'POST',
        body: { chatId: chat.value.trim(), botToken: botToken.value.trim() },
      },
    )
  } catch (err) {
    // A refusal is shown in the answer box beside the button rather than in the
    // banner at the top of the page, which is off screen from down here.
    testAnswer.value = { ok: false, error: apiError(err, 'The message could not be sent.') }
  } finally {
    trying.value = false
  }
}

// The sample body is meant to be pasted somewhere else, so the page hands it
// over as it is. The button says it was copied for a moment, then goes back to
// being pressable.
async function copySample() {
  error.value = ''

  try {
    await navigator.clipboard.writeText(sample.value)
    copied.value = true
    clearTimeout(copiedTimer)
    copiedTimer = setTimeout(() => {
      copied.value = false
    }, 2000)
  } catch {
    error.value = 'The body could not be copied. Select it and copy it by hand.'
  }
}

// A test writes itself down beside the calls that went out on their own, so the
// list under the button is read back rather than added to by hand.
async function refreshCalls() {
  try {
    const res = await $fetch<AlertState>(`/api/v1/vendors/${slug.value}/alerts`)
    if (state.value) state.value.calls = res.calls ?? []
  } catch {
    // The log is there to be helpful. Failing to read it is not worth an error
    // banner over the button that was just pressed.
  }
}
</script>

<template>
  <div class="mx-auto max-w-6xl px-4 py-8">
    <NuxtLink to="/alerts" class="link link-hover text-sm opacity-70">
      &larr; Your alerts
    </NuxtLink>

    <div v-if="vendorError" role="alert" class="alert alert-error mt-6">
      <span>This service could not be loaded right now. The API may be restarting.</span>
    </div>

    <template v-else-if="vendor">
      <header class="mt-4">
        <h1 class="text-2xl font-bold tracking-tight">
          Alerts for {{ vendor.name }}
        </h1>
        <p class="mt-1 text-sm opacity-70">
          One alert per service, arriving by email, by webhook, in a Telegram
          chat, or several of them. An email and a chat report on what you pick;
          a call carries the whole service.
        </p>
      </header>

      <div v-if="nothingArriving" role="alert" class="alert alert-warning mt-4 py-2 text-sm">
        <span>
          Every way of this alert is paused, so nothing is arriving. Start one
          again with its own switch in the panel below.
        </span>
      </div>

      <div v-if="error" role="alert" class="alert alert-error mt-4 py-2 text-sm">
        <span>{{ error }}</span>
      </div>

      <section class="mt-6">
        <h2 class="font-semibold">How it arrives</h2>

        <div class="mt-3 grid gap-4 md:grid-cols-[14rem_1fr]">
          <!-- The ways are a row of tabs on a phone and a menu beside the panel
               once there is room for one. -->
          <div role="tablist" class="tabs tabs-box md:hidden">
            <button
              type="button"
              role="tab"
              class="tab"
              :class="{ 'tab-active': way === 'email' }"
              @click="way = 'email'"
            >
              Email
              <span class="badge badge-xs ml-1 max-sm:hidden" :class="emailWay.cls">
                {{ emailWay.label }}
              </span>
            </button>
            <button
              type="button"
              role="tab"
              class="tab"
              :class="{ 'tab-active': way === 'webhook' }"
              @click="way = 'webhook'"
            >
              Webhook
              <span class="badge badge-xs ml-1 max-sm:hidden" :class="webhookWay.cls">
                {{ webhookWay.label }}
              </span>
            </button>
            <button
              type="button"
              role="tab"
              class="tab"
              :class="{ 'tab-active': way === 'telegram' }"
              @click="way = 'telegram'"
            >
              Telegram
              <span class="badge badge-xs ml-1 max-sm:hidden" :class="telegramWay.cls">
                {{ telegramWay.label }}
              </span>
            </button>
            <button
              type="button"
              role="tab"
              class="tab"
              :class="{ 'tab-active': way === 'rss' }"
              @click="way = 'rss'"
            >
              RSS
            </button>
          </div>

          <!-- The way being shown is the one the menu marks, which daisyUI does
               with menu-active on the item itself rather than on its li. Its
               fill is toned down by menu-quiet in main.css. -->
          <ul class="menu menu-quiet hidden w-56 gap-1 self-start rounded-box border border-base-300 bg-base-100 p-2 md:flex">
            <li>
              <button
                type="button"
                class="justify-between"
                :class="{ 'menu-active': way === 'email' }"
                :aria-current="way === 'email' ? 'true' : undefined"
                @click="way = 'email'"
              >
                Email
                <span class="badge badge-xs" :class="emailWay.cls">
                  {{ emailWay.label }}
                </span>
              </button>
            </li>
            <li>
              <button
                type="button"
                class="justify-between"
                :class="{ 'menu-active': way === 'webhook' }"
                :aria-current="way === 'webhook' ? 'true' : undefined"
                @click="way = 'webhook'"
              >
                Webhook
                <span class="badge badge-xs" :class="webhookWay.cls">
                  {{ webhookWay.label }}
                </span>
              </button>
            </li>
            <li>
              <button
                type="button"
                class="justify-between"
                :class="{ 'menu-active': way === 'telegram' }"
                :aria-current="way === 'telegram' ? 'true' : undefined"
                @click="way = 'telegram'"
              >
                Telegram
                <span class="badge badge-xs" :class="telegramWay.cls">
                  {{ telegramWay.label }}
                </span>
              </button>
            </li>
            <li>
              <button
                type="button"
                class="justify-between"
                :class="{ 'menu-active': way === 'rss' }"
                :aria-current="way === 'rss' ? 'true' : undefined"
                @click="way = 'rss'"
              >
                RSS
              </button>
            </li>
          </ul>

          <div class="min-w-0 rounded-box border border-base-300 bg-base-100 p-5">
            <div v-show="way === 'email'">
              <h3 class="font-semibold">Email</h3>
              <p class="mt-1 text-sm opacity-70">
                An email each time one of the things you pick below changes, in
                either direction. Every email carries a link that stops them.
              </p>

              <input
                v-model="email"
                type="email"
                autocomplete="email"
                placeholder="you@example.com"
                aria-label="Email address"
                class="input input-bordered mt-3 w-full"
              >
              <p v-if="!emailsOn && accountEmail" class="mt-2 text-sm opacity-70">
                <button type="button" class="link" @click="email = accountEmail">
                  Use my account address ({{ accountEmail }})
                </button>
              </p>
              <p class="mt-2 text-sm opacity-70">
                Leave the address empty to stop the emails for good.
              </p>

              <!-- What an email reports on, which is the email's own question: a
                   call carries the whole service whatever is picked here. -->
              <div class="mt-4 rounded-box border border-base-300">
                <div class="flex items-center justify-between border-b border-base-300 px-5 py-3">
                  <span class="text-sm font-medium">What to email about</span>
                  <StatusBadge :status="vendor.status" size="xs" />
                </div>
                <AlertTargets
                  v-model:whole="whole"
                  v-model:picked="picked"
                  :slug="slug"
                  :name="vendor.name"
                  :status="vendor.status"
                  :components="vendor.componentsList ?? []"
                />
              </div>

              <!-- The way's own switch, at the end of the way and just before
                   Save. It is the way being quiet rather than the way being
                   written, so it acts on its own rather than with Save, and a
                   way that has nothing saved yet has nothing to be quiet. -->
              <div class="mt-5 border-t border-base-300 pt-4">
                <div class="flex items-center gap-3">
                  <input
                    id="pause-email"
                    type="checkbox"
                    class="toggle toggle-sm"
                    :checked="!!savedEmail && !emailPaused"
                    :disabled="!savedEmail || pausing === 'email'"
                    @change="setPaused('email', $event)"
                  >
                  <label
                    for="pause-email"
                    class="text-sm"
                    :class="savedEmail ? 'cursor-pointer' : ''"
                  >
                    <template v-if="!savedEmail">
                      Nothing to pause yet.
                      <span class="opacity-70">Save an address and this switch
                        will hold the emails back without taking it away.</span>
                    </template>
                    <template v-else-if="emailPaused">
                      Emails are paused.
                      <span class="opacity-70">Switch this on to hear about changes again.</span>
                    </template>
                    <template v-else>
                      Emails are on.
                      <span class="opacity-70">Switch this off to pause them without taking the address away.</span>
                    </template>
                  </label>
                </div>
              </div>
            </div>

            <div v-show="way === 'webhook'">
              <h3 class="font-semibold">Webhook</h3>
              <p class="mt-1 text-sm opacity-70">
                A call is made each time this service changes, in either direction:
                its own status and every component it reports.
              </p>

              <div class="mt-3 flex flex-col gap-2 sm:flex-row">
                <input
                  v-model="hook.url"
                  type="url"
                  placeholder="https://example.com/hooks/downradar"
                  aria-label="Webhook URL"
                  class="input input-bordered w-full"
                >
                <select v-model="hook.method" aria-label="Method" class="select select-bordered sm:w-28">
                  <option>POST</option>
                  <option>PUT</option>
                  <option>PATCH</option>
                </select>
              </div>
              <p v-if="hookProblem" class="mt-2 text-sm text-error">
                {{ hookProblem }}
              </p>
              <p class="mt-2 text-sm opacity-70">
                Leave the URL empty to stop the calls for good.
              </p>

              <p class="mt-3 text-sm opacity-70">
                Every call carries an <code class="text-xs">Authorization</code> header
                with this alert's own token, unless you write one of your own below.
                Check it at your end to know a call is ours.
              </p>

              <div class="mt-4">
                <AlertFieldList
                  v-model="hook.headers"
                  label="Headers"
                  name-placeholder="X-Name"
                  value-placeholder="value"
                  add-label="Add header"
                />
              </div>

              <div class="mt-4 rounded-box bg-base-200 p-3">
                <p class="text-xs opacity-70">
                  A header value can carry these, which are filled in with what
                  changed before the call is sent.
                </p>
                <ul class="mt-2 space-y-1 text-xs opacity-80">
                  <li v-for="hint in placeholderHint" :key="hint.token">
                    <code class="rounded bg-base-300 px-1">{{ hint.token }}</code>
                    {{ hint.says }}
                  </li>
                </ul>
              </div>

              <details class="mt-4 min-w-0 rounded-box border border-base-300">
                <summary class="cursor-pointer px-4 py-3 text-sm font-medium">
                  What a call carries
                  <!-- The body is long and is meant to be pasted somewhere else,
                       so it is offered from the line that names it. The click is
                       stopped so that copying does not open or close it too. -->
                  <button
                    type="button"
                    class="link float-right text-xs font-normal"
                    @click.stop.prevent="copySample"
                  >
                    {{ copied ? 'Copied.' : 'Copy body' }}
                  </button>
                </summary>
                <div class="min-w-0 border-t border-base-300 px-4 py-3">
                  <p class="text-xs opacity-70">
                    Every call has this shape, so a receiver can read the first one
                    and know the rest. The statuses are the names this site stores
                    them as.
                  </p>
                  <pre class="mt-2 overflow-x-auto rounded-box bg-base-200 p-3 text-xs">{{ sample }}</pre>
                </div>
              </details>

              <!-- What the webhook has actually been doing, rather than only
                   what it is set to. The body that was sent is not kept, so this
                   cannot show anything a subscriber should not see. -->
              <div v-if="calls.length" class="mt-4 min-w-0">
                <div class="flex items-center justify-between gap-3">
                  <h4 class="text-sm font-medium">Recent calls</h4>
                  <span class="text-xs opacity-60">newest first, the last ten</span>
                </div>
                <ul class="mt-2 divide-y divide-base-300 rounded-box border border-base-300">
                  <li v-for="call in calls" :key="call.id" class="min-w-0 p-3">
                    <div class="flex flex-wrap items-center gap-2 text-xs">
                      <span class="badge badge-xs" :class="statusClass(call.status)">
                        {{ call.status ?? 'no answer' }}
                      </span>
                      <span class="font-medium">{{ call.method }}</span>
                      <span class="opacity-60">{{ formatDate(call.createdAt) }}</span>
                      <span v-if="call.kind === 'test'" class="badge badge-xs badge-ghost">test</span>
                    </div>
                    <p v-if="call.error" class="mt-1 text-xs text-error">{{ call.error }}</p>
                    <pre
                      v-if="call.response"
                      class="mt-1 max-h-32 overflow-auto whitespace-pre-wrap break-all rounded-box bg-base-200 p-2 text-xs"
                    >{{ call.response }}</pre>
                  </li>
                </ul>
              </div>

              <!-- The way's own switch and the call that tries it, at the end
                   of the way and just before Save. The switch says the way is
                   working, so it is the way being quiet rather than the way
                   being written, and it acts on its own. -->
              <div class="mt-5 border-t border-base-300 pt-4">
                <div class="flex items-center gap-3">
                  <input
                    id="pause-webhook"
                    type="checkbox"
                    class="toggle toggle-sm"
                    :checked="!!savedWebhook && !webhookPaused"
                    :disabled="!savedWebhook || pausing === 'webhook'"
                    @change="setPaused('webhook', $event)"
                  >
                  <label
                    for="pause-webhook"
                    class="text-sm"
                    :class="savedWebhook ? 'cursor-pointer' : ''"
                  >
                    <template v-if="!savedWebhook">
                      Nothing to pause yet.
                      <span class="opacity-70">Save a URL and this switch will
                        hold the calls back without taking it away.</span>
                    </template>
                    <template v-else-if="webhookPaused">
                      Calls are paused.
                      <span class="opacity-70">Switch this on to hear about changes again.</span>
                    </template>
                    <template v-else>
                      Calls are on.
                      <span class="opacity-70">Switch this off to pause them without taking the URL away.</span>
                    </template>
                  </label>
                </div>

                <div class="mt-3 flex flex-wrap items-center gap-3">
                  <button
                    type="button"
                    class="btn btn-outline btn-sm"
                    :disabled="testing || !callsOn || !!hookProblem"
                    @click="tryWebhook"
                  >
                    <span v-if="testing" class="loading loading-spinner loading-xs" />
                    Send a test call
                  </button>
                  <span v-if="!testResult" class="text-xs opacity-70">
                    <template v-if="hookProblem">The URL above has to be fixed first.</template>
                    <template v-else-if="callsOn">
                      Uses the URL, method and headers above, saved or not, with a
                      made up change in it.
                    </template>
                    <template v-else>A call needs a URL above.</template>
                  </span>
                </div>

                <div
                  v-if="testResult"
                  class="mt-3 min-w-0 rounded-box border border-base-300 bg-base-200 p-3"
                >
                  <p class="flex flex-wrap items-center gap-2 text-sm">
                    <span class="font-medium">What the far end answered</span>
                    <span class="badge badge-sm" :class="statusClass(testResult.status)">
                      {{ testResult.status ?? 'no answer' }}
                    </span>
                  </p>
                  <p v-if="testResult.error" class="mt-2 text-sm text-error">
                    {{ testResult.error }}
                  </p>
                  <pre
                    v-if="testResult.response"
                    class="mt-2 max-h-48 overflow-auto whitespace-pre-wrap break-all rounded-box bg-base-100 p-3 text-xs"
                  >{{ testResult.response }}</pre>
                  <p v-else-if="!testResult.error" class="mt-2 text-sm opacity-70">
                    It answered with nothing.
                  </p>
                </div>
              </div>
            </div>

            <div v-show="way === 'telegram'">
              <h3 class="font-semibold">Telegram</h3>
              <p class="mt-1 text-sm opacity-70">
                A message in a chat each time one of the things you pick below
                changes, in either direction.
              </p>

              <template v-if="telegramEnabled">
                <p class="mt-3 text-sm opacity-70">
                  Make a bot of your own with
                  <span class="font-medium">@BotFather</span> in Telegram and paste
                  the token it gives you here. Then press Start on that bot, since
                  a bot can only message a chat that started it.
                </p>

                <!-- The class goes on a wrapper rather than on the field: a
                     PasswordInput hands its attributes to the input itself. -->
                <div class="mt-3">
                  <PasswordInput
                    v-model="botToken"
                    label="Bot token"
                    secret="bot token"
                    autocomplete="off"
                    placeholder="123456789:AAH..."
                  >
                    <span class="mt-1 text-sm opacity-70">
                      The token is kept with this alert, and only you can see it.
                    </span>
                  </PasswordInput>
                </div>

                <input
                  v-model="chat"
                  type="text"
                  placeholder="123456789 or -1001234567890"
                  aria-label="Chat id"
                  class="input input-bordered mt-3 w-full"
                >
                <p class="mt-2 text-sm opacity-70">
                  The chat's number goes here: @userinfobot gives you yours, and for
                  a group, add your bot to the group first. Leave the chat empty to
                  stop the messages for good.
                </p>

                <div class="mt-3 flex flex-wrap items-center gap-3">
                  <button
                    type="button"
                    class="btn btn-outline btn-sm"
                    :disabled="trying || !chat.trim() || !botToken.trim()"
                    @click="tryTelegram"
                  >
                    <span v-if="trying" class="loading loading-spinner loading-xs" />
                    Send a test message
                  </button>
                  <span v-if="!testAnswer" class="text-xs opacity-70">
                    A message goes to this chat now, saved or not.
                  </span>
                </div>

                <div
                  v-if="testAnswer"
                  class="mt-3 rounded-box border border-base-300 bg-base-200 p-3"
                >
                  <p class="flex flex-wrap items-center gap-2 text-sm">
                    <span class="font-medium">What Telegram answered</span>
                    <span class="badge badge-sm" :class="testAnswer.ok ? 'badge-success' : 'badge-error'">
                      {{ testAnswer.ok ? 'delivered' : 'refused' }}
                    </span>
                  </p>
                  <p v-if="testAnswer.error" class="mt-2 text-sm text-error">
                    {{ testAnswer.error }}
                  </p>
                  <p v-else class="mt-2 text-sm opacity-70">
                    The message arrived in that chat.
                  </p>
                </div>
              </template>

              <p v-else class="mt-3 text-sm opacity-70">
                Telegram is not available right now, so there is nowhere for a
                message to go. Try again later.
              </p>

              <!-- The way's own switch, at the end of the way and just before
                   Save, the same as the other two. -->
              <div class="mt-5 border-t border-base-300 pt-4">
                <div class="flex items-center gap-3">
                  <input
                    id="pause-telegram"
                    type="checkbox"
                    class="toggle toggle-sm"
                    :checked="!!savedTelegram && !telegramPaused"
                    :disabled="!savedTelegram || pausing === 'telegram'"
                    @change="setPaused('telegram', $event)"
                  >
                  <label
                    for="pause-telegram"
                    class="text-sm"
                    :class="savedTelegram ? 'cursor-pointer' : ''"
                  >
                    <template v-if="!savedTelegram">
                      Nothing to pause yet.
                      <span class="opacity-70">Save a chat and this switch will
                        hold the messages back without taking it away.</span>
                    </template>
                    <template v-else-if="telegramPaused">
                      Messages are paused.
                      <span class="opacity-70">Switch this on to hear about changes again.</span>
                    </template>
                    <template v-else>
                      Messages are on.
                      <span class="opacity-70">Switch this off to pause them without taking the chat away.</span>
                    </template>
                  </label>
                </div>
              </div>
            </div>

            <!-- The feed is the one way here that nothing is kept for, so it has
                 no switch and no field to save: what it is, is an address. It
                 sits last because it is the way with nothing to set up. -->
            <div v-show="way === 'rss'">
              <h3 class="font-semibold">RSS</h3>
              <AlertFeed class="mt-1" :slug="slug" :name="vendor.name" />
            </div>
          </div>

          <!-- One Save writes the whole alert, so its row sits in the column of
               the card it writes rather than across the page. The feed panel has
               nothing to write, so the row is not there while it is open. -->
          <div v-if="way !== 'rss'" class="md:col-start-2">
            <div class="flex flex-wrap items-center justify-end gap-3">
              <span v-if="saved" class="text-sm text-success">Saved.</span>
              <button
                type="button"
                class="btn btn-primary btn-sm"
                :disabled="busy || nothingToTellAbout || (!anyWay && !state?.subscribed) || !!hookProblem"
                @click="save"
              >
                <span v-if="busy" class="loading loading-spinner loading-xs" />
                Save
              </button>
            </div>

            <p v-if="nothingToTellAbout" class="mt-2 text-sm opacity-70">
              Pick at least one thing to be told about.
            </p>
            <p v-else-if="!anyWay && !state?.subscribed" class="mt-2 text-sm opacity-70">
              An alert needs an address, a webhook or a chat to arrive on.
            </p>
          </div>
        </div>
      </section>
    </template>

    <p v-else-if="loading" class="mt-6 text-sm opacity-70">Loading…</p>
  </div>
</template>
