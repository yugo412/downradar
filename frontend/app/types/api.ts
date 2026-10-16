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

export interface VendorDetail extends Vendor {
  aliases?: string[]
  componentsList: ComponentStatus[]
  recentEvents: StatusEvent[]
  incidents: Incident[]
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
  active: boolean
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
  email: string
  createdAt: string
  unsubscribedAt?: string | null
}

// What the bell asks about the signed in visitor: whether their account already
// has an alert on this vendor, the masked address it goes to, and whether it is
// paused.
export interface AlertState {
  subscribed: boolean
  email?: string
  paused?: boolean
}

// One row on the account's alerts screen.
export interface AccountAlert {
  id: number
  vendorSlug: string
  vendorName: string
  vendorStatus: Health
  email: string
  state: 'active' | 'paused' | 'unsubscribed'
  createdAt: string
}export interface ServicePayload {
  name: string
  url: string
  profile: string
  category: string
  logoUrl: string
  aliases: string[]
  status?: ServiceStatus
  visibility?: Visibility
}
