export function timeAgo(iso?: string | null): string {
  if (!iso) return '—'
  const then = new Date(iso).getTime()
  if (Number.isNaN(then)) return '—'

  const seconds = Math.max(0, Math.floor((Date.now() - then) / 1000))
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
