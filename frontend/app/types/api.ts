export interface Vendor {
  slug: string
  name: string
  url: string
  category?: string
  logoUrl?: string
  status: Health
  changedAt?: string | null
  components: number
  degradedComponents: number
  lastPolledAt?: string | null
}

export interface ComponentStatus {
  sourceId: string
  name: string
  group?: string
  position: number
  status: Health
  changedAt?: string | null
}

export interface StatusEvent {
  status: Health
  component?: string
  changedAt: string
}

export interface Incident {
  sourceId: string
  name: string
  status: string
  impact?: string
  startedAt?: string | null
  resolvedAt?: string | null
  url?: string
}

export interface GeneratedSummary {
  text: string
  generatedAt: string
}

export interface VendorDetail extends Vendor {
  aliases?: string[]
  componentsList: ComponentStatus[]
  recentEvents: StatusEvent[]
  incidents: Incident[]
  generatedSummary?: GeneratedSummary | null
}

export interface PollRun {
  id: number
  ok: boolean
  httpStatus?: number
  componentsFound: number
  unknownStatuses?: string[]
  error?: string
  durationMs?: number
  fetchedAt: string
}

export interface Stats {
  total: number
  healthy: number
  degraded: number
  down: number
  unknown: number
}

export type Health = 'ok' | 'degraded' | 'partial' | 'major' | 'maintenance' | 'unknown'

export type Role = 'admin' | 'moderator' | 'subscriber'

export interface AuthUser {
  id: number
  email: string
  name: string
  role: Role
  verifiedAt?: string | null
  // False for an account made through a provider, which has no password to
  // change, only one to set.
  hasPassword?: boolean
}

// One way to sign in that this deployment has configured. The screens offer the
// buttons from this list, so a provider without credentials is not a button that
// fails when it is pressed.
export interface AuthProvider {
  name: string
  label: string
}

// A configured provider on the account screen, with whether this account has
// already attached it and which identity the provider gave.
export interface LinkedLogin extends AuthProvider {
  connected: boolean
  email?: string
  username?: string
}

export type ServiceStatus = 'proposed' | 'published' | 'rejected' | 'no_match'

export interface AdminService {
  id: number
  slug: string
  name: string
  url: string
  category?: string
  logoUrl?: string
  profile: string
  endpoint?: string
  aliases: string[]
  allowSharedPage: boolean
  pollIntervalSeconds: number
  activeIntervalSeconds: number
  status: ServiceStatus
  state: string
  active: boolean
  visibility: Visibility
  ownerId?: number
  ownerName?: string
  ownerEmail?: string
  lastPolledAt?: string | null
  createdAt: string
  updatedAt: string
  components: number
  hasHistory: boolean
}

export type Visibility = 'public' | 'private'

export interface AdminUser {
  id: number
  email: string
  name: string
  role: Role
  verifiedAt?: string | null
  disabledAt?: string | null
  services: number
  alerts: number
  createdAt: string
  updatedAt: string
}

export interface AdminProfile {
  name: string
  kind: string
  description?: string
  endpoint?: string
  pollIntervalSeconds: number
  activeIntervalSeconds: number
  visible: boolean
}

export interface ServiceCreated {
  slug: string
  status: ServiceStatus
  profile: string
  visibility: Visibility
}

// What the unsubscribe page is told about the link it was opened with. The email
// is masked: the page is reached through a link that can be forwarded.
export interface AlertSubscription {
  vendorSlug: string
  vendorName: string
  kind: string
  destination: string
  createdAt: string
  paused?: boolean
  unsubscribedAt?: string | null
}

// What the bell asks about the signed in visitor: whether their account already
// has an alert on this vendor, the masked address it goes to, whether it is
// paused, and what it watches, so the modal opens with what they picked ticked.
// Components are named by the identifiers the API takes them back as.
// One name and value in a webhook: a header, or a pair in a form body.
export interface AlertField {
  name: string
  value: string
}

// The second way an alert can arrive: where to call, and the headers it is
// called with. The header values may carry placeholders, which the server fills
// in with what changed. What a call carries in its body is the server's, so
// every call has the same shape and a receiver can read the first one.
//
// Auth is how a call says it is ours: standard puts a Standard Webhooks
// signature on it, custom sends the headers alone. Signed says whether a secret
// is set. The secret itself is only ever in the answer that made it.
export interface AlertWebhook {
  url: string
  method: string
  headers: AlertField[]
  auth: 'standard' | 'custom'
  signed: boolean
}

// One call this alert made, and what came back. A call that never got there has
// no status, and says why in the error beside it. The body that was sent and the
// headers are not kept, so a token never sits in this table.
export interface WebhookCall {
  id: number
  kind: 'delivery' | 'test'
  url: string
  method: string
  status: number | null
  response: string
  error: string
  createdAt: string
}

export interface ChannelDeliveryStatus {
  kind: string
  attemptAt?: string | null
  successAt?: string | null
  error?: string
}

export interface AlertState {
  subscribed: boolean
  email?: string
  paused?: boolean
  // Whether a way that is set up is quiet on its own. One of them can be paused
  // without the other, which is how a way is stopped without being taken away.
  emailPaused?: boolean
  webhookPaused?: boolean
  telegramPaused?: boolean
  slackPaused?: boolean
  discordPaused?: boolean
  larkPaused?: boolean
  wholeService?: boolean
  components?: string[]
  webhook?: AlertWebhook | null
  // The chat a message goes to, the bot that speaks to it, and whether the site
  // has a sender loop at all. The bot belongs to the subscriber, and its token is
  // shown masked on their own page with a switch to reveal it.
  telegram?: string
  telegramBot?: string
  telegramEnabled?: boolean
  // The incoming webhook URL of the Slack channel, which the page shows masked
  // with a switch to reveal it, the same way the chat's bot token is shown. The
  // name is what an install left behind, so the page can say which channel the
  // messages go to; a URL pasted by hand has none. SlackEnabled says whether the
  // site has a Slack app to install in one click.
  slack?: string
  slackChannel?: string
  slackEnabled?: boolean
  // The incoming webhook URL of the Discord channel, shown masked like the Slack
  // one. A Discord channel is set up by hand, so there is no install to name.
  discord?: string
  // The custom bot webhook of a Lark group and the secret that signs its calls
  // when the group requires a signature. Both are shown masked on the page.
  lark?: string
  larkSecret?: string
  calls?: WebhookCall[]
  // The last real delivery of each channel, not a history. Tests do not move it.
  channelStatus?: ChannelDeliveryStatus[]
}

// One row on the account's alerts screen. A row watches the vendor's own status,
// some of its components, or both, so it carries what it watches: the components
// by name, which is what the screen shows.
export interface AccountAlert {
  id: number
  vendorSlug: string
  vendorName: string
  vendorStatus: Health
  email: string
  state: 'active' | 'paused' | 'unsubscribed'
  createdAt: string
  wholeService: boolean
  components: string[]
  channels: string[]
}

// One row on the staff alerts screen: the subscription, plus who left it. A
// guest has no account, so their name is empty and the address stands alone.
// It is read only, so it carries no actions.
export interface StaffAlert {
  id: number
  subscriberName: string
  subscriberEmail: string
  guest: boolean
  vendorSlug: string
  vendorName: string
  vendorStatus: Health
  state: 'active' | 'paused' | 'unsubscribed'
  createdAt: string
  wholeService: boolean
  components: string[]
  channels: string[]
}

export interface ServicePayload {
  name: string
  // Staff correct the address; a subscriber's payload never carries one.
  slug?: string
  url: string
  profile: string
  category: string
  logoUrl: string
  aliases: string[]
  status?: ServiceStatus
  visibility?: Visibility
}
