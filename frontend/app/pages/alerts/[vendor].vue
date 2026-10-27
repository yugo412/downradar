<script setup lang="ts">
import type { AlertState, AlertWebhook, VendorDetail } from '~/types/api'

definePageMeta({ middleware: 'authenticated', ssr: false })

useSeoMeta({ title: 'Alert', robots: 'noindex, nofollow' })

const { user } = useAuth()

const route = useRoute()
const router = useRouter()
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
type Way = 'email' | 'webhook' | 'telegram' | 'slack' | 'discord' | 'lark' | 'rss'

function blankWebhook(): AlertWebhook {
  return { url: '', method: 'POST', headers: [], auth: 'standard', signed: false }
}

const state = ref<AlertState | null>(null)
const loading = ref(true)
const busy = ref(false)
const saved = ref(false)
const error = ref('')

// Trying a webhook, and what came back from the one that was tried.
const testing = ref(false)
const testResult = ref<{ status: number | null; response: string; error: string } | null>(null)

// The signing secret dialog. The secret it shows is held here only until the
// dialog closes: the site hands it out once, in the answer that made it.
const secretOpen = ref(false)
const secretBusy = ref(false)
const secretError = ref('')
const secretValue = ref('')
const secretRotating = ref(false)

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

// The webhook settings, the body it carries and the calls it made are three
// different questions, so the panel gives each one a tab instead of one long
// column.
type WebhookTab = 'alert' | 'sample' | 'log'
const webhookTab = ref<WebhookTab>('alert')

// The panel that is open is part of the address, so a link to one opens it. The
// hash is the way's own name: #email, #webhook, #telegram, #slack, #discord,
// #lark, #rss.
const ways = ['email', 'webhook', 'telegram', 'slack', 'discord', 'lark', 'rss'] as const

function isWay(value: string): value is Way {
  return (ways as readonly string[]).includes(value)
}

function hashWay(): Way | null {
  const hash = route.hash.replace(/^#/, '')
  return isWay(hash) ? hash : null
}

function selectWay(next: Way) {
  way.value = next
  if (route.hash !== '#' + next) {
    router.replace({ hash: '#' + next })
  }
}

// A hand-edited address or a back button changes the hash without going through
// selectWay, so the panel follows it.
watch(
  () => route.hash,
  (hash) => {
    const next = hash.replace(/^#/, '')
    if (isWay(next) && next !== way.value) way.value = next
  },
)

// The Slack way is a URL rather than a chat: the incoming webhook Slack makes for
// the channel. It is a secret, so the field masks it and offers to show it.
const slackURL = ref('')

// The Discord way is the same kind of thing: the incoming webhook Discord makes
// for a channel, kept masked like the Slack one.
const discordURL = ref('')

// Lark is the custom bot webhook of a group and, when the group requires it, the
// secret that signs each call.
const larkURL = ref('')
const larkSecret = ref('')

// Trying the chat, and what the chat service answered. It is a message like a
// delivery, so what comes back is whether it arrived rather than a body.
const trying = ref(false)
const testAnswer = ref<{ ok: boolean; error: string } | null>(null)

// Trying the Slack channel, which answers whether the message arrived.
const slackTrying = ref(false)
const slackAnswer = ref<{ ok: boolean; error: string } | null>(null)

// Trying the Discord channel, which answers the same way.
const discordTrying = ref(false)
const discordAnswer = ref<{ ok: boolean; error: string } | null>(null)

// Trying the Lark group, whose answer includes its own code when it refuses.
const larkTrying = ref(false)
const larkAnswer = ref<{ ok: boolean; code: number; error: string } | null>(null)

// Disconnecting is a save of its own: it stops the Slack way and leaves the
// other ways and what the alert watches exactly as they were.
const disconnecting = ref(false)

// What an install answered, in the address the callback sent the browser back
// with. It is read once and taken out of the address, so a reload does not say
// it again. The channel itself arrives with the alert, not here.
const slackNotice = ref<{ ok: boolean; text: string } | null>(null)

// What the email reports on. A call reports on the whole service, whatever is
// picked here, because its body says what the service looks like now.
const whole = ref(true)
const picked = ref<string[]>([])

const emailsOn = computed(() => email.value.trim() !== '')
const callsOn = computed(() => hook.value.url.trim() !== '')
const chatsOn = computed(() => chat.value.trim() !== '')
const slackOn = computed(() => slackURL.value.trim() !== '')
const discordOn = computed(() => discordURL.value.trim() !== '')
const larkOn = computed(() => larkURL.value.trim() !== '')
const anyWay = computed(
  () =>
    emailsOn.value ||
    callsOn.value ||
    chatsOn.value ||
    slackOn.value ||
    discordOn.value ||
    larkOn.value,
)

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

// What is wrong with the Slack URL as it stands. An empty one is not wrong: that
// is how the messages are stopped. Anything else has to be an incoming webhook
// URL of the kind Slack itself makes for a channel.
const slackProblem = computed(() => {
  const url = slackURL.value.trim()
  if (url === '') return ''
  if (url.length > 1024) return 'That Slack webhook URL is too long.'

  let parsed: URL
  try {
    parsed = new URL(url)
  } catch {
    return 'That does not look like a Slack webhook URL.'
  }
  const host = parsed.hostname
  if (
    parsed.protocol !== 'https:' ||
    (host !== 'hooks.slack.com' && host !== 'hooks.slack-gov.com') ||
    !parsed.pathname.startsWith('/services/')
  ) {
    return 'Paste the incoming webhook URL Slack gives you, starting with https://hooks.slack.com/services/.'
  }
  return ''
})

// What is wrong with the Discord URL as it stands. Discord's own webhooks are
// the only kind that can be pasted here, so the host and path are checked too
// rather than only that the string is an address.
const discordProblem = computed(() => {
  const url = discordURL.value.trim()
  if (url === '') return ''
  if (url.length > 1024) return 'That Discord webhook URL is too long.'

  let parsed: URL
  try {
    parsed = new URL(url)
  } catch {
    return 'That does not look like a Discord webhook URL.'
  }
  const host = parsed.hostname
  const discordHost =
    host === 'discord.com' ||
    host === 'discordapp.com' ||
    host === 'ptb.discord.com' ||
    host === 'canary.discord.com'
  if (
    parsed.protocol !== 'https:' ||
    !discordHost ||
    !parsed.pathname.startsWith('/api/webhooks/')
  ) {
    return 'Paste the incoming webhook URL Discord gives you, starting with https://discord.com/api/webhooks/.'
  }
  return ''
})

// What is wrong with the Lark webhook as it stands. Lark and Feishu use their own
// hosts, so a URL that is only an address is not enough to look like a bot.
const larkProblem = computed(() => {
  const url = larkURL.value.trim()
  if (url === '') return ''
  if (url.length > 1024) return 'That Lark webhook URL is too long.'
  if (larkSecret.value.trim().length > 512) return 'That Lark signing secret is too long.'

  let parsed: URL
  try {
    parsed = new URL(url)
  } catch {
    return 'That does not look like a Lark webhook URL.'
  }
  if (
    parsed.protocol !== 'https:' ||
    (parsed.hostname !== 'open.larksuite.com' && parsed.hostname !== 'open.feishu.cn') ||
    !parsed.pathname.startsWith('/open-apis/bot/v2/hook/')
  ) {
    return 'Paste the custom bot webhook from Lark, starting with https://open.larksuite.com/open-apis/bot/v2/hook/.'
  }
  return ''
})

// An address, a chat, a Slack channel, a Discord channel or a Lark group with nothing picked
// beside it would be a way of being told nothing, since each reports on what was
// picked.
const nothingToTellAbout = computed(
  () =>
    (emailsOn.value || chatsOn.value || slackOn.value || discordOn.value || larkOn.value) &&
    !whole.value &&
    picked.value.length === 0,
)

// What the saved ways are, which is what a switch can be asked about: a way has
// to exist before it can be paused, and what the fields hold and have not been
// saved yet is not a way yet.
const savedEmail = computed(() => String(state.value?.email ?? ''))
const savedWebhook = computed(() => String(state.value?.webhook?.url ?? ''))
const savedTelegram = computed(() => String(state.value?.telegram ?? ''))
const savedSlack = computed(() => String(state.value?.slack ?? ''))
const slackChannel = computed(() => String(state.value?.slackChannel ?? ''))
const savedDiscord = computed(() => String(state.value?.discord ?? ''))
const savedLark = computed(() => String(state.value?.lark ?? ''))
const emailPaused = computed(() => state.value?.emailPaused === true)
const webhookPaused = computed(() => state.value?.webhookPaused === true)
const telegramPaused = computed(() => state.value?.telegramPaused === true)
const slackPaused = computed(() => state.value?.slackPaused === true)
const discordPaused = computed(() => state.value?.discordPaused === true)
const larkPaused = computed(() => state.value?.larkPaused === true)
const larkDelivery = computed(() =>
  (state.value?.channelStatus ?? []).find((status) => status.kind === 'lark'),
)

// Whether the site has a Slack app to install in one click. Without one the
// panel keeps the manual webhook path and says why the button is not offered.
const slackEnabled = computed(() => state.value?.slackEnabled === true)

// A Slack message that reports on nothing would never have anything to say.
const slackNothingToPost = computed(() => !whole.value && picked.value.length === 0)

// Whether the site has a sender loop at all, which is what decides if a chat is
// offered.
const telegramEnabled = computed(() => state.value?.telegramEnabled === true)

// Nothing arrives when every way the alert has is quiet. The account screen and
// the bell already call that paused; the banner says the same here.
const nothingArriving = computed(() => {
  const ways = [
    { set: savedEmail.value !== '', paused: emailPaused.value },
    { set: savedWebhook.value !== '', paused: webhookPaused.value },
    { set: savedTelegram.value !== '', paused: telegramPaused.value },
    { set: savedSlack.value !== '', paused: slackPaused.value },
    { set: savedDiscord.value !== '', paused: discordPaused.value },
    { set: savedLark.value !== '', paused: larkPaused.value },
  ].filter((way) => way.set)
  return state.value?.subscribed === true && ways.length > 0 && ways.every((way) => way.paused)
})

// The address on the account, offered as a one click fill in the email panel
// while the field is empty.
const accountEmail = computed(() => String(user.value?.email ?? ''))

// The calls this alert made lately, which is what the webhook panel shows under
// the switch: what it does, rather than only what it is set to.
const calls = computed(() => state.value?.calls ?? [])

// The log keeps the last fifty calls, so it is read twenty at a time. The page
// clamps rather than letting a shorter log leave the reader on an empty page.
const CALL_PAGE_SIZE = 20
const callPage = ref(1)
const callPages = computed(() => Math.max(1, Math.ceil(calls.value.length / CALL_PAGE_SIZE)))
const currentCallPage = computed(() => Math.min(callPage.value, callPages.value))
const pagedCalls = computed(() =>
  calls.value.slice(
    (currentCallPage.value - 1) * CALL_PAGE_SIZE,
    currentCallPage.value * CALL_PAGE_SIZE,
  ),
)

watch(
  () => calls.value.length,
  () => {
    if (callPage.value > callPages.value) callPage.value = callPages.value
  },
)

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

onMounted(async () => {
  await load()

  const result = String(route.query.slack ?? '')
  if (result) {
    showSlackResult()
    return
  }

  const next = hashWay()
  if (next) way.value = next

  // Keep the address naming the panel that is open, even when the panel was
  // picked by what the alert already has.
  if (route.hash !== '#' + way.value) {
    router.replace({ hash: '#' + way.value })
  }
})

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
    slackURL.value = res.subscribed ? res.slack ?? '' : ''
    discordURL.value = res.subscribed ? res.discord ?? '' : ''
    larkURL.value = res.subscribed ? res.lark ?? '' : ''
    larkSecret.value = res.subscribed ? res.larkSecret ?? '' : ''
    testAnswer.value = null
    slackAnswer.value = null
    discordAnswer.value = null
    larkAnswer.value = null

    whole.value = res.subscribed ? res.wholeService ?? true : true
    picked.value = res.subscribed ? res.components ?? [] : []

    // The way that is already set up decides which panel opens.
    if (
      res.subscribed &&
      !res.email &&
      res.webhook &&
      !res.telegram &&
      !res.slack &&
      !res.discord
    ) {
      way.value = 'webhook'
    } else if (
      res.subscribed &&
      !res.email &&
      !res.webhook &&
      res.telegram &&
      !res.slack &&
      !res.discord
    ) {
      way.value = 'telegram'
    } else if (
      res.subscribed &&
      !res.email &&
      !res.webhook &&
      !res.telegram &&
      res.slack &&
      !res.discord
    ) {
      way.value = 'slack'
    } else if (
      res.subscribed &&
      !res.email &&
      !res.webhook &&
      !res.telegram &&
      !res.slack &&
      res.discord &&
      !res.lark
    ) {
      way.value = 'discord'
    } else if (
      res.subscribed &&
      !res.email &&
      !res.webhook &&
      !res.telegram &&
      !res.slack &&
      !res.discord &&
      res.lark
    ) {
      way.value = 'lark'
    }
  } catch (err) {
    error.value = apiError(err, 'Could not load this alert.')
  } finally {
    loading.value = false
  }
}

// What the callback said when Slack sent the browser back. The reason is in the
// address rather than in the answer, because the install is a navigation. It is
// read once and taken out of the address so a reload does not say it again.
function showSlackResult() {
  const result = String(route.query.slack ?? '')
  if (!result) return

  const messages: Record<string, { ok: boolean; text: string }> = {
    connected: {
      ok: true,
      text: 'Slack is connected. The channel is saved with this alert.',
    },
    refused: {
      ok: false,
      text: 'Slack was not connected. Nothing changed.',
    },
    limit: {
      ok: false,
      text: 'Your account has reached its limit of services. Remove one from your alerts page first.',
    },
    pick: {
      ok: false,
      text: 'Pick the whole service or at least one component before connecting Slack.',
    },
    'too-many': {
      ok: false,
      text: 'This alert picks too many things to connect in one click. Pick the whole service instead.',
    },
    unavailable: {
      ok: false,
      text: 'Adding to Slack is not set up on this site. Paste a webhook URL below instead.',
    },
    failed: {
      ok: false,
      text: 'Slack could not be connected. Try again.',
    },
  }
  slackNotice.value = messages[result] ?? messages.failed
  way.value = 'slack'

  const query = { ...route.query }
  delete query.slack
  router.replace({ query, hash: '#slack' })
}

// One click installs the site's Slack app. What the page holds is saved first,
// so an address or a chat typed but not saved yet is not left behind by the trip
// to Slack. A page with no other way has nothing to save before the callback
// writes the channel with the picks below.
async function connectSlack() {
  if (!slackEnabled.value || slackNothingToPost.value || busy.value) return
  if (hookProblem.value || slackProblem.value || discordProblem.value || larkProblem.value) return

  if (anyWay.value) {
    await save()
    if (busy.value || error.value || secretOpen.value) return
  }

  const query = new URLSearchParams({ wholeService: String(whole.value) })
  for (const id of picked.value) query.append('components', id)
  window.location.href =
    `/api/v1/vendors/${encodeURIComponent(slug.value)}/alerts/slack/connect?` +
    query.toString()
}

// Stopping the channel is the same save the page already makes, with an empty
// URL and the targets the alert already has. Naming only Slack leaves the other
// ways alone, so nothing else about the alert changes.
async function disconnectSlack() {
  if (!savedSlack.value || disconnecting.value) return

  disconnecting.value = true
  slackNotice.value = null

  try {
    await $fetch(`/api/v1/vendors/${slug.value}/alerts`, {
      method: 'POST',
      body: {
        slack: { url: '' },
        wholeService: state.value?.wholeService ?? true,
        components: state.value?.components ?? [],
      },
    })
    await load()
    slackNotice.value = {
      ok: true,
      text: 'Slack is disconnected. Nothing else about this alert changed.',
    }
  } catch (err) {
    slackNotice.value = {
      ok: false,
      text: apiError(err, 'Could not disconnect Slack.'),
    }
  } finally {
    disconnecting.value = false
  }
}

// What a save sends: the whole alert every time, so what is off stays off. An
// empty URL is a webhook, a Slack channel, a Discord channel or a Lark group being stopped
// rather than one being left as it was, and the same goes for the chat.
// generateSecret asks for a fresh signing secret in the same save, which is the
// only way one is made.
function alertBody(generateSecret = false) {
  return {
    email: email.value.trim(),
    webhook: callsOn.value
      ? {
          url: hook.value.url.trim(),
          method: hook.value.method,
          headers: hook.value.headers,
          auth: hook.value.auth,
          generateSecret,
        }
      : { url: '' },
    telegram: { chatId: chat.value.trim(), botToken: botToken.value.trim() },
    slack: { url: slackURL.value.trim() },
    discord: { url: discordURL.value.trim() },
    lark: { url: larkURL.value.trim(), secret: larkSecret.value.trim() },
    wholeService: whole.value,
    components: picked.value,
  }
}

type SaveAnswer = { status: string; webhookSecret?: string }

async function save() {
  if (hookProblem.value || slackProblem.value || discordProblem.value || larkProblem.value) return

  // A signed webhook needs a secret before it can be saved. The dialog is opened
  // instead of refusing at the banner: making the secret is the same save, and
  // the page says so there.
  if (callsOn.value && hook.value.auth === 'standard' && !hook.value.signed) {
    openSecret()
    return
  }

  busy.value = true
  error.value = ''
  saved.value = false

  try {
    await $fetch<SaveAnswer>(`/api/v1/vendors/${slug.value}/alerts`, {
      method: 'POST',
      body: alertBody(),
    })
    await load()
    saved.value = true
  } catch (err) {
    error.value = apiError(err)
  } finally {
    busy.value = false
  }
}

function openSecret() {
  secretRotating.value = hook.value.signed
  secretValue.value = ''
  secretError.value = ''
  secretOpen.value = true
}

// Closing the dialog is the end of the secret: what it showed is dropped here
// rather than kept for a second look. Making another one is the only way back.
function closeSecret() {
  secretOpen.value = false
  secretValue.value = ''
  secretError.value = ''
}

async function generateSecret() {
  if (hookProblem.value || slackProblem.value || discordProblem.value || larkProblem.value || !callsOn.value) return

  secretBusy.value = true
  secretError.value = ''

  try {
    const res = await $fetch<SaveAnswer>(`/api/v1/vendors/${slug.value}/alerts`, {
      method: 'POST',
      body: alertBody(true),
    })
    if (!res.webhookSecret) {
      secretError.value = 'The site made no secret. Try again.'
      return
    }
    secretValue.value = res.webhookSecret
    await load()
    saved.value = true
  } catch (err) {
    secretError.value = apiError(err, 'Could not make a signing secret.')
  } finally {
    secretBusy.value = false
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
    else if (way === 'telegram') current.telegramPaused = !on
    else if (way === 'slack') current.slackPaused = !on
    else if (way === 'discord') current.discordPaused = !on
    else if (way === 'lark') current.larkPaused = !on
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
          auth: hook.value.auth,
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

// A message is sent to the Slack channel the page is holding, saved or not, so a
// channel can be checked before it is relied on. What comes back is whether it
// arrived, and what Slack said when it did not.
async function trySlack() {
  if (!slackOn.value && !savedSlack.value) return

  slackTrying.value = true
  slackAnswer.value = null
  error.value = ''

  try {
    slackAnswer.value = await $fetch<{ ok: boolean; error: string }>(
      `/api/v1/vendors/${slug.value}/alerts/slack/test`,
      {
        method: 'POST',
        body: { url: slackURL.value.trim() },
      },
    )
  } catch (err) {
    // A refusal is shown in the answer box beside the button rather than in the
    // banner at the top of the page, which is off screen from down here.
    slackAnswer.value = { ok: false, error: apiError(err, 'The message could not be sent.') }
  } finally {
    slackTrying.value = false
  }
}

// A message is sent to the Discord channel the page is holding, saved or not, so
// a channel can be checked before it is relied on. What comes back is whether it
// arrived, and what Discord said when it did not.
async function tryDiscord() {
  if (!discordOn.value && !savedDiscord.value) return

  discordTrying.value = true
  discordAnswer.value = null
  error.value = ''

  try {
    discordAnswer.value = await $fetch<{ ok: boolean; error: string }>(
      `/api/v1/vendors/${slug.value}/alerts/discord/test`,
      {
        method: 'POST',
        body: { url: discordURL.value.trim() },
      },
    )
  } catch (err) {
    // A refusal is shown in the answer box beside the button rather than in the
    // banner at the top of the page, which is off screen from down here.
    discordAnswer.value = { ok: false, error: apiError(err, 'The message could not be sent.') }
  } finally {
    discordTrying.value = false
  }
}

// A message is sent to the Lark group the page is holding, saved or not, so a
// group can be checked before it is relied on. Lark answers with its own code
// when it refuses.
async function tryLark() {
  if (!larkOn.value && !savedLark.value) return

  larkTrying.value = true
  larkAnswer.value = null
  error.value = ''

  try {
    larkAnswer.value = await $fetch<{ ok: boolean; code: number; error: string }>(
      `/api/v1/vendors/${slug.value}/alerts/lark/test`,
      {
        method: 'POST',
        body: { url: larkURL.value.trim(), secret: larkSecret.value.trim() },
      },
    )
  } catch (err) {
    larkAnswer.value = {
      ok: false,
      code: 0,
      error: apiError(err, 'The message could not be sent.'),
    }
  } finally {
    larkTrying.value = false
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
    callPage.value = 1
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
          One alert per service, delivered by email, webhook, Telegram, Slack,
          Discord, Lark or a mix of them. Email, Telegram, Slack, Discord and
          Lark report on what you pick. A webhook always carries the whole service.
        </p>
      </header>

      <div v-if="nothingArriving" role="alert" class="alert alert-warning mt-4 py-2 text-sm">
        <span>
          Every way of this alert is paused, so nothing is arriving. Start one
          with the switch beside its name.
        </span>
      </div>

      <div v-if="error" role="alert" class="alert alert-error mt-4 py-2 text-sm">
        <span>{{ error }}</span>
      </div>

      <section class="mt-6">
        <h2 class="font-semibold">How it arrives</h2>

        <div class="mt-3 grid w-full gap-4 md:grid-cols-[14rem_minmax(0,1fr)]">
          <!-- The way being shown is the one the list marks. Each way has its
               own pause switch, and a way that is not set up has none to move. -->
          <div class="w-full self-start md:w-56">
            <div class="rounded-box border border-base-300 bg-base-100 p-2">
              <ul class="flex w-full flex-col gap-1 p-0">
                <AlertWayRow
                  label="Email"
                  icon="email"
                  :active="way === 'email'"
                  :on="savedEmail !== ''"
                  :paused="emailPaused"
                  :busy="pausing === 'email'"
                  @select="selectWay('email')"
                  @pause="setPaused('email', $event)"
                />
                <AlertWayRow
                  label="Webhook"
                  icon="webhook"
                  :active="way === 'webhook'"
                  :on="savedWebhook !== ''"
                  :paused="webhookPaused"
                  :busy="pausing === 'webhook'"
                  @select="selectWay('webhook')"
                  @pause="setPaused('webhook', $event)"
                />
                <AlertWayRow
                  label="Telegram"
                  icon="telegram"
                  :active="way === 'telegram'"
                  :on="savedTelegram !== ''"
                  :paused="telegramPaused"
                  :busy="pausing === 'telegram'"
                  @select="selectWay('telegram')"
                  @pause="setPaused('telegram', $event)"
                />
                <AlertWayRow
                  label="Slack"
                  icon="slack"
                  :active="way === 'slack'"
                  :on="savedSlack !== ''"
                  :paused="slackPaused"
                  :busy="pausing === 'slack'"
                  @select="selectWay('slack')"
                  @pause="setPaused('slack', $event)"
                />
                <AlertWayRow
                  label="Discord"
                  icon="discord"
                  :active="way === 'discord'"
                  :on="savedDiscord !== ''"
                  :paused="discordPaused"
                  :busy="pausing === 'discord'"
                  @select="selectWay('discord')"
                  @pause="setPaused('discord', $event)"
                />
                <AlertWayRow
                  label="Lark"
                  icon="lark"
                  :active="way === 'lark'"
                  :on="savedLark !== ''"
                  :paused="larkPaused"
                  :busy="pausing === 'lark'"
                  @select="selectWay('lark')"
                  @pause="setPaused('lark', $event)"
                />
                <li>
                  <button
                    type="button"
                    class="flex w-full items-center gap-2 rounded-btn px-3 py-2 text-left text-sm"
                    :class="way === 'rss' ? 'bg-base-200 font-medium' : 'hover:bg-base-200'"
                    :aria-current="way === 'rss' ? 'true' : undefined"
                    @click="selectWay('rss')"
                  >
                    <ChannelIcon kind="rss" />
                    <span>RSS</span>
                  </button>
                </li>
              </ul>
            </div>
          </div>

          <div class="min-w-0 w-full overflow-hidden rounded-box border border-base-300 bg-base-100 p-5">
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
                <button type="button" class="link break-all text-left" @click="email = accountEmail">
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

            </div>

            <div v-show="way === 'webhook'">
              <h3 class="font-semibold">Webhook</h3>
              <p class="mt-1 text-sm opacity-70">
                A call is made each time this service changes, in either direction:
                its own status and every component it reports.
              </p>

              <div role="tablist" aria-label="Webhook sections" class="tabs tabs-box mt-4 w-full">
                <button
                  id="webhook-tab-alert"
                  type="button"
                  role="tab"
                  class="tab"
                  :class="{ 'tab-active': webhookTab === 'alert' }"
                  :aria-selected="webhookTab === 'alert'"
                  aria-controls="webhook-panel-alert"
                  @click="webhookTab = 'alert'"
                >
                  Alert
                </button>
                <button
                  id="webhook-tab-sample"
                  type="button"
                  role="tab"
                  class="tab"
                  :class="{ 'tab-active': webhookTab === 'sample' }"
                  :aria-selected="webhookTab === 'sample'"
                  aria-controls="webhook-panel-sample"
                  @click="webhookTab = 'sample'"
                >
                  Sample Response
                </button>
                <button
                  id="webhook-tab-log"
                  type="button"
                  role="tab"
                  class="tab"
                  :class="{ 'tab-active': webhookTab === 'log' }"
                  :aria-selected="webhookTab === 'log'"
                  aria-controls="webhook-panel-log"
                  @click="webhookTab = 'log'"
                >
                  Webhook Log
                  <span v-if="calls.length" class="badge badge-xs badge-ghost">
                    {{ calls.length }}
                  </span>
                </button>
              </div>

              <div
                v-show="webhookTab === 'alert'"
                id="webhook-panel-alert"
                role="tabpanel"
                aria-labelledby="webhook-tab-alert"
                class="mt-4"
              >
                <!-- The part that has to be right for a call to arrive at all:
                     where it goes, how it is proved to be ours, and the secret that
                     proves it. What an arriving call looks like is below. -->
                <fieldset class="mt-3 min-w-0 rounded-box border border-base-300 p-4">
                  <legend class="px-1 text-sm font-semibold">Where and how it calls</legend>

                  <div class="join w-full">
                    <input
                      v-model="hook.url"
                      type="url"
                      placeholder="https://example.com/hooks/downradar"
                      aria-label="Webhook URL"
                      class="input input-bordered join-item min-w-0 flex-1"
                    >
                    <select
                      v-model="hook.method"
                      aria-label="Method"
                      class="select select-bordered join-item w-24 sm:w-28"
                    >
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

                  <!-- How a call says it is ours. Standard is the one to pick: it
                       puts a signature on every call. Custom stays for a service
                       that only takes a static header or a URL. -->
                  <div class="mt-4">
                    <p class="text-sm font-medium">Authentication</p>
                    <div class="mt-2 flex flex-col gap-2">
                      <label
                        class="flex cursor-pointer items-start gap-3 rounded-box border p-3"
                        :class="hook.auth === 'standard' ? 'border-primary bg-base-200' : 'border-base-300'"
                      >
                        <input v-model="hook.auth" type="radio" value="standard" class="radio radio-sm mt-0.5">
                        <span class="text-sm">
                          <span class="font-medium">Standard (Recommended)</span>
                          <span class="mt-0.5 block opacity-70">
                            Every call carries a signature made with a secret only you and this site
                            know, so your end can tell a real call from anything else that finds its
                            way there. Read the
                            <a
                              class="link"
                              href="https://github.com/standard-webhooks/standard-webhooks/blob/main/spec/standard-webhooks.md"
                              target="_blank"
                              rel="noreferrer"
                              >specification</a
                            >.
                          </span>
                        </span>
                      </label>
                      <label
                        class="flex cursor-pointer items-start gap-3 rounded-box border p-3"
                        :class="hook.auth === 'custom' ? 'border-primary bg-base-200' : 'border-base-300'"
                      >
                        <input v-model="hook.auth" type="radio" value="custom" class="radio radio-sm mt-0.5">
                        <span class="text-sm">
                          <span class="font-medium">Custom headers</span>
                          <span class="mt-0.5 block opacity-70">
                            Only the headers below are sent, for a service that takes a static header or
                            a URL and cannot be changed to check a signature.
                          </span>
                        </span>
                      </label>
                    </div>
                  </div>

                  <!-- The secret a signed call is made with. The page only ever
                       knows whether one is set: it is shown once, in the dialog
                       that makes it, and the site never hands it out again. -->
                  <div v-if="hook.auth === 'standard'" class="mt-3 rounded-box border border-base-300 p-3">
                    <div class="flex flex-wrap items-center justify-between gap-3">
                      <div class="min-w-0">
                        <p class="text-sm font-medium">Signing secret</p>
                        <p class="text-sm opacity-70">
                          <template v-if="hook.signed">
                            A secret is set. Every call is signed with it.
                          </template>
                          <template v-else>
                            No secret yet. One is needed before a signed call can go out.
                          </template>
                        </p>
                      </div>
                      <button
                        type="button"
                        class="btn btn-outline btn-sm"
                        :disabled="!callsOn || !!hookProblem"
                        @click="openSecret"
                      >
                        {{ hook.signed ? 'Regenerate secret' : 'Generate secret' }}
                      </button>
                    </div>
                    <p v-if="!callsOn" class="mt-2 text-xs opacity-70">
                      Fill in the URL first: the secret is saved with the webhook.
                    </p>
                  </div>

                  <!-- What a signed call carries beyond the body, so the headers a
                       receiver has to look at are on the page that sets them up. -->
                  <details
                    v-if="hook.auth === 'standard'"
                    class="mt-3 min-w-0 rounded-box border border-base-300"
                  >
                    <summary class="cursor-pointer px-4 py-3 text-sm font-medium">
                      What a signed call adds
                    </summary>
                    <div class="min-w-0 border-t border-base-300 px-4 py-3">
                      <p class="text-xs opacity-70">
                        Three headers beside your own. The signature is HMAC-SHA256 over the
                        id, the timestamp and the body, keyed with the signing secret.
                      </p>
                      <pre
                        class="mt-2 overflow-x-auto rounded-box bg-base-200 p-3 text-xs"
                      >webhook-id: msg_2KWPBgLlAfxdpx2AI54pPJ85f4W
  webhook-timestamp: 1674087231
  webhook-signature: v1,g0hM9SsE+OTPJTGt/tmIKtSyZlE3uFJELVlNIOLJ1OE=</pre>
                    </div>
                  </details>
                </fieldset>

                <!-- What the call carries beside its body. The headers are the
                     subscriber's in both modes, and one of them is always ours. -->
                <fieldset class="mt-4 min-w-0 rounded-box border border-base-300 p-4">
                  <legend class="px-1 text-sm font-semibold">Headers</legend>

                  <p class="text-sm opacity-70">
                    <template v-if="hook.auth === 'standard'">
                      The signature is what proves a call is ours, so nothing is added here. Write
                      whatever the service asks for, such as a key of its own.
                    </template>
                    <template v-else>
                      Nothing signs a call in this mode, so these are what your end can check.
                      Write a key of your own if the service needs one.
                    </template>
                  </p>

                  <div class="mt-3">
                    <AlertFieldList
                      v-model="hook.headers"
                      label="Headers"
                      name-placeholder="X-Name"
                      value-placeholder="value"
                      add-label="Add header"
                      :show-label="false"
                    />
                    <p class="mt-2 text-xs opacity-70">
                      Sent with every call, whichever way it is authenticated.
                    </p>
                  </div>

                  <details class="mt-3 min-w-0 rounded-box border border-base-300">
                    <summary class="cursor-pointer px-4 py-3 text-sm font-medium">
                      Header placeholders
                    </summary>
                    <div class="border-t border-base-300 px-4 py-3">
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
                  </details>
                </fieldset>

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

              <div
                v-show="webhookTab === 'sample'"
                id="webhook-panel-sample"
                role="tabpanel"
                aria-labelledby="webhook-tab-sample"
                class="mt-4"
              >
                <div class="flex flex-wrap items-start justify-between gap-4">
                  <div class="min-w-0 max-w-2xl">
                    <p class="text-sm font-medium">Response body</p>
                    <p class="mt-1 text-xs opacity-70">
                      Every call has this shape, so a receiver can read the first one
                      and know the rest. The statuses are the names this site stores
                      them as.
                    </p>
                  </div>
                  <button
                    type="button"
                    class="btn btn-outline btn-sm shrink-0 gap-1.5"
                    @click="copySample"
                  >
                    <svg
                      xmlns="http://www.w3.org/2000/svg"
                      class="h-3.5 w-3.5"
                      viewBox="0 0 24 24"
                      fill="none"
                      stroke="currentColor"
                      stroke-width="2"
                      stroke-linecap="round"
                      stroke-linejoin="round"
                      aria-hidden="true"
                    >
                      <rect width="14" height="14" x="8" y="8" rx="2" ry="2" />
                      <path d="M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2" />
                    </svg>
                    {{ copied ? 'Copied' : 'Copy body' }}
                  </button>
                </div>
                <div class="mockup-code mt-3 min-w-0 overflow-x-auto text-xs">
                  <pre><code>{{ sample }}</code></pre>
                </div>
              </div>

              <div
                v-show="webhookTab === 'log'"
                id="webhook-panel-log"
                role="tabpanel"
                aria-labelledby="webhook-tab-log"
                class="mt-4"
              >
                <div
                  v-if="!calls.length"
                  class="rounded-box border border-base-300 p-4 text-sm opacity-70"
                >
                  No calls yet. Send a test call from the Alert tab to see what your end answers.
                </div>
                <template v-else>
                  <div class="flex flex-wrap items-center justify-between gap-3">
                    <h4 class="text-sm font-medium">Recent calls</h4>
                    <span class="text-xs opacity-60">newest first, 20 per page</span>
                  </div>
                  <ul class="mt-2 divide-y divide-base-300 rounded-box border border-base-300">
                    <li v-for="call in pagedCalls" :key="call.id" class="min-w-0 p-3">
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
                  <div
                    v-if="callPages > 1"
                    class="mt-3 flex flex-wrap items-center justify-between gap-3"
                  >
                    <button
                      type="button"
                      class="btn btn-sm"
                      :disabled="currentCallPage <= 1"
                      @click="callPage = currentCallPage - 1"
                    >
                      Previous
                    </button>
                    <span class="text-xs opacity-60">
                      Page {{ currentCallPage }} of {{ callPages }}
                    </span>
                    <button
                      type="button"
                      class="btn btn-sm"
                      :disabled="currentCallPage >= callPages"
                      @click="callPage = currentCallPage + 1"
                    >
                      Next
                    </button>
                  </div>
                </template>
              </div>
            </div>

            <div v-show="way === 'telegram'">
              <h3 class="font-semibold">Telegram</h3>
              <p class="mt-1 text-sm opacity-70">
                A message in a chat each time one of the things you pick below
                changes, in either direction.
              </p>

              <template v-if="telegramEnabled">
                <details class="mt-3 min-w-0 rounded-box border border-base-300">
                  <summary class="cursor-pointer px-4 py-3 text-sm font-medium">
                    How to make a bot
                  </summary>
                  <div class="min-w-0 border-t border-base-300 px-4 py-3">
                    <ol class="list-decimal space-y-1 pl-5 text-sm opacity-70">
                      <li>
                        Make a bot of your own with
                        <span class="font-medium">@BotFather</span> in Telegram, then
                        paste the token it gives you here.
                      </li>
                      <li>
                        Press Start on that bot, since a bot can only message a chat
                        that started it.
                      </li>
                      <li>
                        Get the chat's number from
                        <span class="font-medium">@userinfobot</span>. For a group,
                        add your bot to the group first.
                      </li>
                    </ol>
                  </div>
                </details>

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
                  The chat's number, from @userinfobot. Leave it empty to stop the
                  messages for good.
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

            </div>

            <div v-show="way === 'slack'">
              <h3 class="font-semibold">Slack</h3>
              <p class="mt-1 text-sm opacity-70">
                A message in a Slack channel each time one of the things you pick
                below changes, in either direction.
              </p>

              <!-- Installing the site's Slack app is the way this is meant to be
                   set up: Slack asks which channel, and the webhook it makes is
                   saved here. The manual field below is the other way, for a
                   reader who already has a webhook of their own. -->
              <div
                v-if="slackNotice"
                :role="slackNotice.ok ? 'status' : 'alert'"
                class="alert mt-4 py-2 text-sm"
                :class="slackNotice.ok ? 'alert-success' : 'alert-error'"
              >
                <span>{{ slackNotice.text }}</span>
              </div>

              <div
                class="mt-4"
                :class="slackChannel ? 'rounded-box border border-base-300 p-4' : ''"
              >
                <div class="flex flex-wrap items-center gap-3">
                  <p v-if="slackChannel" class="min-w-0 text-sm">
                    Connected to
                    <span class="font-medium">{{ slackChannel }}</span>. Connect
                    again to move it.
                  </p>
                  <div
                    class="flex flex-wrap items-center gap-2"
                    :class="slackChannel ? 'ml-auto' : ''"
                  >
                    <button
                      type="button"
                      class="btn btn-sm shrink-0"
                      :disabled="!slackEnabled || slackNothingToPost || busy"
                      @click="connectSlack"
                    >
                      <SlackIcon class="size-4 shrink-0" />
                      {{ slackChannel ? 'Change channel' : 'Add to Slack' }}
                    </button>
                    <button
                      v-if="slackChannel"
                      type="button"
                      class="btn btn-ghost btn-sm text-error"
                      :disabled="disconnecting || busy"
                      @click="disconnectSlack"
                    >
                      <span
                        v-if="disconnecting"
                        class="loading loading-spinner loading-xs"
                      />
                      Disconnect
                    </button>
                  </div>
                </div>
                <p v-if="!slackEnabled" class="mt-3 text-xs opacity-70">
                  Signing in to Slack is not set up on this site. Paste a webhook
                  URL below.
                </p>
                <p v-else-if="slackNothingToPost" class="mt-3 text-xs text-error">
                  Pick the whole service or a component below first.
                </p>
              </div>

              <!-- A channel added in one click has no URL to paste and nothing to
                   look up, so the manual way is not shown beside it. A URL that
                   was pasted by hand keeps the field, because that is how it is
                   checked or replaced. -->
              <template v-if="!slackChannel">
                <div class="mt-5 flex items-center gap-3 text-xs opacity-70">
                  <span class="h-px flex-1 bg-base-300" />
                  or paste a webhook URL
                  <span class="h-px flex-1 bg-base-300" />
                </div>

                <details class="mt-3 min-w-0 rounded-box border border-base-300">
                  <summary class="cursor-pointer px-4 py-3 text-sm font-medium">
                    How to make a webhook
                  </summary>
                  <div class="min-w-0 border-t border-base-300 px-4 py-3">
                    <ol class="list-decimal space-y-1 pl-5 text-sm opacity-70">
                      <li>
                        Open
                        <a
                          href="https://api.slack.com/apps"
                          class="link"
                          target="_blank"
                          rel="noopener"
                        >api.slack.com/apps</a>
                        and create an app from scratch.
                      </li>
                      <li>In the app, open Incoming Webhooks and switch it on.</li>
                      <li>
                        Add a webhook to the workspace, pick the channel, then copy
                        the URL it gives you here.
                      </li>
                    </ol>
                    <p class="mt-2 text-sm opacity-70">
                      Anybody who may add apps to the workspace can make one.
                    </p>
                  </div>
                </details>

                <!-- The URL is a secret, so the field masks it and offers to show
                     it, the same way the chat's bot token is shown. -->
                <div class="mt-3">
                  <PasswordInput
                    v-model="slackURL"
                    label="Webhook URL"
                    secret="webhook URL"
                    autocomplete="off"
                    placeholder="https://hooks.slack.com/services/..."
                  >
                    <p v-if="slackProblem" class="mt-2 text-sm text-error">{{ slackProblem }}</p>
                    <p class="mt-2 text-sm opacity-70">
                      The URL is kept with this alert and shown masked. Leave it
                      empty to stop the messages for good.
                    </p>
                  </PasswordInput>
                </div>
              </template>

              <!-- What a Slack message reports on, which is what the subscriber
                   picked: the message names the component that moved. -->
              <div class="mt-4 rounded-box border border-base-300">
                <div class="flex items-center justify-between border-b border-base-300 px-5 py-3">
                  <span class="text-sm font-medium">What to post about</span>
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

              <div class="mt-3 flex flex-wrap items-center gap-3">
                <button
                  type="button"
                  class="btn btn-outline btn-sm"
                  :disabled="slackTrying || (!slackOn && !savedSlack) || !!slackProblem"
                  @click="trySlack"
                >
                  <span v-if="slackTrying" class="loading loading-spinner loading-xs" />
                  Send a test message
                </button>
                <span v-if="!slackAnswer" class="text-xs opacity-70">
                  A message goes to this channel now, saved or not.
                </span>
              </div>

              <div
                v-if="slackAnswer"
                class="mt-3 rounded-box border border-base-300 bg-base-200 p-3"
              >
                <p class="flex flex-wrap items-center gap-2 text-sm">
                  <span class="font-medium">What Slack answered</span>
                  <span
                    class="badge badge-sm"
                    :class="slackAnswer.ok ? 'badge-success' : 'badge-error'"
                  >
                    {{ slackAnswer.ok ? 'delivered' : 'refused' }}
                  </span>
                </p>
                <p v-if="slackAnswer.error" class="mt-2 text-sm text-error">
                  {{ slackAnswer.error }}
                </p>
                <p v-else class="mt-2 text-sm opacity-70">
                  The message arrived in that channel.
                </p>
              </div>

            </div>

            <div v-show="way === 'discord'">
              <h3 class="font-semibold">Discord</h3>
              <p class="mt-1 text-sm opacity-70">
                A post in a Discord channel each time one of the things you pick
                below changes, in either direction. The post links to the status
                page and to a page that stops these messages.
              </p>

              <details class="mt-3 min-w-0 rounded-box border border-base-300">
                <summary class="cursor-pointer px-4 py-3 text-sm font-medium">
                  How to make a webhook
                </summary>
                <div class="min-w-0 border-t border-base-300 px-4 py-3">
                  <ol class="list-decimal space-y-1 pl-5 text-sm opacity-70">
                    <li>
                      Open the channel's settings in Discord and choose
                      <span class="font-medium">Integrations</span>.
                    </li>
                    <li>
                      Open <span class="font-medium">Webhooks</span> and press
                      <span class="font-medium">New Webhook</span>.
                    </li>
                    <li>
                      Pick the channel, copy the webhook URL, and paste it here.
                    </li>
                  </ol>
                  <p class="mt-2 text-sm opacity-70">
                    You need Manage Webhooks in that channel to make one.
                  </p>
                </div>
              </details>

              <!-- The URL is a secret, so the field masks it and offers to show
                   it, the same way the Slack one is shown. -->
              <div class="mt-3">
                <PasswordInput
                  v-model="discordURL"
                  label="Webhook URL"
                  secret="webhook URL"
                  autocomplete="off"
                  placeholder="https://discord.com/api/webhooks/..."
                >
                  <p v-if="discordProblem" class="mt-2 text-sm text-error">{{ discordProblem }}</p>
                  <p class="mt-2 text-sm opacity-70">
                    The URL is kept with this alert and shown masked. Leave it
                    empty to stop the messages for good.
                  </p>
                </PasswordInput>
              </div>

              <!-- What a Discord message reports on, which is what the
                   subscriber picked: the post names the component that moved. -->
              <div class="mt-4 rounded-box border border-base-300">
                <div class="flex items-center justify-between border-b border-base-300 px-5 py-3">
                  <span class="text-sm font-medium">What to post about</span>
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

              <div class="mt-3 flex flex-wrap items-center gap-3">
                <button
                  type="button"
                  class="btn btn-outline btn-sm"
                  :disabled="discordTrying || (!discordOn && !savedDiscord) || !!discordProblem"
                  @click="tryDiscord"
                >
                  <span v-if="discordTrying" class="loading loading-spinner loading-xs" />
                  Send a test message
                </button>
                <span v-if="!discordAnswer" class="text-xs opacity-70">
                  A message goes to this channel now, saved or not.
                </span>
              </div>

              <div
                v-if="discordAnswer"
                class="mt-3 rounded-box border border-base-300 bg-base-200 p-3"
              >
                <p class="flex flex-wrap items-center gap-2 text-sm">
                  <span class="font-medium">What Discord answered</span>
                  <span
                    class="badge badge-sm"
                    :class="discordAnswer.ok ? 'badge-success' : 'badge-error'"
                  >
                    {{ discordAnswer.ok ? 'delivered' : 'refused' }}
                  </span>
                </p>
                <p v-if="discordAnswer.error" class="mt-2 text-sm text-error">
                  {{ discordAnswer.error }}
                </p>
                <p v-else class="mt-2 text-sm opacity-70">
                  The message arrived in that channel.
                </p>
              </div>

            </div>

            <div v-show="way === 'lark'">
              <h3 class="font-semibold">Lark</h3>
              <p class="mt-1 text-sm opacity-70">
                A card in a Lark group each time one of the things you pick below
                changes, in either direction. The card links to the status page
                and to a page that stops these messages.
              </p>

              <details class="mt-3 min-w-0 rounded-box border border-base-300">
                <summary class="cursor-pointer px-4 py-3 text-sm font-medium">
                  How to make a custom bot
                </summary>
                <div class="min-w-0 border-t border-base-300 px-4 py-3">
                  <ol class="list-decimal space-y-1 pl-5 text-sm opacity-70">
                    <li>
                      In the Lark group, open Settings, then Bots, and add a
                      <span class="font-medium">Custom Bot</span>.
                    </li>
                    <li>
                      Copy the webhook URL the bot gives you and paste it here.
                    </li>
                    <li>
                      If the bot uses signature verification, copy its secret too.
                    </li>
                  </ol>
                </div>
              </details>

              <!-- The webhook and its secret are both credentials, so the fields
                   mask them and offer to show them, like the other channels. -->
              <div class="mt-3">
                <PasswordInput
                  v-model="larkURL"
                  label="Webhook URL"
                  secret="webhook URL"
                  autocomplete="off"
                  placeholder="https://open.larksuite.com/open-apis/bot/v2/hook/..."
                >
                  <p v-if="larkProblem" class="mt-2 text-sm text-error">{{ larkProblem }}</p>
                  <p class="mt-2 text-sm opacity-70">
                    The URL is kept with this alert and shown masked. Leave it
                    empty to stop the messages for good.
                  </p>
                </PasswordInput>
              </div>

              <div class="mt-3">
                <PasswordInput
                  v-model="larkSecret"
                  label="Signing secret"
                  secret="signing secret"
                  autocomplete="off"
                  placeholder="Only if the bot requires a signed request"
                >
                  <p class="mt-2 text-sm opacity-70">
                    Optional. Paste the secret from the bot's signature
                    verification setting. It is kept with this alert and shown
                    masked.
                  </p>
                </PasswordInput>
              </div>

              <div class="mt-4 rounded-box border border-base-300">
                <div class="flex items-center justify-between border-b border-base-300 px-5 py-3">
                  <span class="text-sm font-medium">What to post about</span>
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

              <div class="mt-3 flex flex-wrap items-center gap-3">
                <button
                  type="button"
                  class="btn btn-outline btn-sm"
                  :disabled="larkTrying || (!larkOn && !savedLark) || !!larkProblem"
                  @click="tryLark"
                >
                  <span v-if="larkTrying" class="loading loading-spinner loading-xs" />
                  Send a test message
                </button>
                <span v-if="!larkAnswer" class="text-xs opacity-70">
                  A message goes to this group now, saved or not.
                </span>
              </div>

              <div
                v-if="larkAnswer"
                class="mt-3 rounded-box border border-base-300 bg-base-200 p-3"
              >
                <p class="flex flex-wrap items-center gap-2 text-sm">
                  <span class="font-medium">What Lark answered</span>
                  <span
                    class="badge badge-sm"
                    :class="larkAnswer.ok ? 'badge-success' : 'badge-error'"
                  >
                    {{ larkAnswer.ok ? 'delivered' : 'refused' }}
                  </span>
                  <span v-if="larkAnswer.code" class="badge badge-sm badge-ghost">
                    code {{ larkAnswer.code }}
                  </span>
                </p>
                <p v-if="larkAnswer.error" class="mt-2 text-sm text-error">
                  {{ larkAnswer.error }}
                </p>
                <p v-else class="mt-2 text-sm opacity-70">
                  The message arrived in that group.
                </p>
              </div>

              <div
                v-if="larkDelivery?.attemptAt"
                class="mt-3 rounded-box border border-base-300 bg-base-200 p-3"
              >
                <template v-if="larkDelivery.error">
                  <p class="text-sm font-medium text-error">
                    Last delivery failed
                    <LiveAgo :at="larkDelivery.attemptAt" />
                  </p>
                  <p class="mt-1 break-words text-xs text-error">
                    {{ larkDelivery.error }}
                  </p>
                </template>
                <p v-else class="text-sm">
                  <span class="font-medium text-success">Last delivered</span>
                  <LiveAgo :at="larkDelivery.successAt ?? larkDelivery.attemptAt" />
                </p>
              </div>

            </div>

            <!-- The feed is the one way here that nothing is kept for, so it has
                 no field to save: what it is, is an address. It sits last
                 because it is the way with nothing to set up. -->
            <div v-show="way === 'rss'" class="w-full">
              <h3 class="font-semibold">RSS</h3>
              <AlertFeed class="mt-1 w-full" :slug="slug" :name="vendor.name" />
            </div>
          </div>

          <!-- One Save writes the whole alert, so its row sits in the column of
               the card it writes rather than across the page. The feed panel has
               nothing to write, so the row is not there while it is open. -->
          <div v-if="way !== 'rss'" class="md:col-start-2">
            <div class="flex flex-wrap items-center justify-end gap-3">
              <span v-if="saved" class="text-sm text-success">Saved</span>
              <button
                type="button"
                class="btn btn-primary btn-sm"
                :disabled="busy || nothingToTellAbout || (!anyWay && !state?.subscribed) || !!hookProblem || !!slackProblem || !!discordProblem || !!larkProblem"
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
              An alert needs an address, a webhook, a chat, a Slack channel, a
              Discord channel or a Lark group to arrive on.
            </p>
          </div>
        </div>
      </section>
    </template>

    <p v-else-if="loading" class="mt-6 text-sm opacity-70">Loading…</p>

    <WebhookSecretDialog
      v-if="secretOpen"
      :secret="secretValue"
      :busy="secretBusy"
      :error="secretError"
      :rotating="secretRotating"
      @generate="generateSecret"
      @close="closeSecret"
    />
  </div>
</template>
