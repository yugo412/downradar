// The reference time is a parameter so a caller with a ticking clock can pass
// its own, which is what makes a rendered value move.
export function timeAgo(iso?: string | null, now: number = Date.now()): string {
  if (!iso) return '—'
  const then = new Date(iso).getTime()
  if (Number.isNaN(then)) return '—'

  const seconds = Math.max(0, Math.floor((now - then) / 1000))
  if (seconds < 60) return `${seconds}s ago`

  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes}m ago`

  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h ago`

  const days = Math.floor(hours / 24)
  if (days < 30) return `${days}d ago`

  return new Date(iso).toISOString().slice(0, 10)
}

export function formatDate(iso?: string | null): string {
  if (!iso) return '—'
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return '—'
  return d.toISOString().replace('T', ' ').slice(0, 16) + ' UTC'
}

const SEVERITY: Record<string, number> = {
  major: 5,
  partial: 4,
  degraded: 3,
  unknown: 2,
  maintenance: 1,
  ok: 0,
}

export function severity(status: string): number {
  return SEVERITY[status] ?? 0
}

export function isHealthy(status: string): boolean {
  return status === 'ok' || status === 'maintenance'
}

// The words and the colours a health status is shown with. They live here so the
// status badge, the filter rows built out of it and the colour a name is written
// in cannot drift apart. `cls` fills a badge; `ink` is the same state as a text
// colour, for places that have no badge to fill.
const STATUS_META: Record<string, { label: string; cls: string; ink: string }> = {
  ok: { label: 'Operational', cls: 'badge-success', ink: 'text-success' },
  maintenance: { label: 'Maintenance', cls: 'badge-info', ink: 'text-info' },
  degraded: { label: 'Degraded', cls: 'badge-warning', ink: 'text-warning' },
  partial: { label: 'Partial outage', cls: 'badge-warning', ink: 'text-warning' },
  major: { label: 'Major outage', cls: 'badge-error', ink: 'text-error' },
  unknown: { label: 'Unknown', cls: 'badge-ghost', ink: 'text-base-content/60' },
}

export function statusMeta(status: string): { label: string; cls: string; ink: string } {
  return STATUS_META[status] ?? STATUS_META.unknown!
}

// The states a page can name. Kept beside the words they are shown with, so a URL
// naming one the app does not know is dropped rather than left as a picked filter
// with nothing under it.
export const HEALTH_STATES = Object.keys(STATUS_META)

export interface StatusCount {
  status: string
  count: number
}

// The states a list can be filtered by, each with how many of its rows are in
// that state, worst first. A state that is already picked is kept even at zero,
// so it cannot vanish from under the click that is about to unpick it.
export function statusCounts<T>(
  items: T[],
  statusOf: (item: T) => string,
  picked: string[] = [],
): StatusCount[] {
  const counts = new Map<string, number>()
  for (const item of items) {
    const status = statusOf(item)
    counts.set(status, (counts.get(status) ?? 0) + 1)
  }
  for (const status of picked) {
    if (!counts.has(status)) counts.set(status, 0)
  }

  return [...counts.entries()]
    .map(([status, count]) => ({ status, count }))
    .sort((a, b) => severity(b.status) - severity(a.status) || a.status.localeCompare(b.status))
}
