// The zone is found once in the browser, after the page has hydrated. The first
// render is UTC on both sides, so a cached page cannot hand one visitor's zone
// to the next one, and hydration has the same text to compare with.
const zone = ref('UTC')
let detected = false

export function useLocalTime() {
  if (import.meta.client) {
    onMounted(() => {
      if (detected) return
      detected = true
      try {
        zone.value = Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
      } catch {
        zone.value = 'UTC'
      }
    })
  }

  return {
    zone,
    format: (iso?: string | null) => formatDate(iso, zone.value),
  }
}
