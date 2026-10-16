import type { ServiceStatus } from '~/types/api'

// The management vocabulary, which is a different question from the health a
// vendor reports: a row can be published and still answer nothing.
export function serviceStatusLabel(status: ServiceStatus, active = true): string {
  if (!active) return 'removed'
  if (status === 'no_match') return 'no match'
  return status
}

export function serviceStatusClass(status: ServiceStatus, active = true): string {
  if (!active) return 'badge-ghost'
  if (status === 'published') return 'badge-success'
  if (status === 'proposed') return 'badge-warning'
  if (status === 'no_match') return 'badge-ghost'
  return 'badge-error'
}
