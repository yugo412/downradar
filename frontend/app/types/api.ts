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

export interface Stats {
  total: number
  healthy: number
  degraded: number
  down: number
  unknown: number
}

export type Health = 'ok' | 'degraded' | 'partial' | 'major' | 'maintenance' | 'unknown'
