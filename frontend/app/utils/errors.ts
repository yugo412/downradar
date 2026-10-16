export function apiError(err: unknown, fallback = 'Something went wrong. Please try again.'): string {
  const data = (err as { data?: { error?: string } })?.data
  return data?.error ?? fallback
}
