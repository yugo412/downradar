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

// The order the management list arrives in and the stage tabs are laid out in: a
// suggestion waiting for a decision, then one nothing can read, then the live
// ones, then the dead ends. Sorting by status wears that same order, so the
// column and the tabs above it cannot disagree.
const STATUS_RANK: Record<string, number> = {
  proposed: 0,
  'no match': 1,
  published: 2,
  rejected: 3,
  removed: 4,
}

export function serviceStatusRank(status: ServiceStatus, active = true): number {
  return STATUS_RANK[serviceStatusLabel(status, active)] ?? 99
}
