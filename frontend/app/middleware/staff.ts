// Client side only. The service pages belong to the people who run the
// catalogue, so a subscriber is sent to the alerts they look after instead. The
// server half is in session-guard.ts, and the API refuses these routes by role
// no matter what the browser does.
export default defineNuxtRouteMiddleware(async (to) => {
  if (import.meta.server) return

  const { user, loaded, load, canManageServices } = useAuth()

  if (!loaded.value) {
    await load()
  }

  if (!user.value) {
    return navigateTo(`/login?next=${encodeURIComponent(to.fullPath)}`)
  }
  if (!canManageServices.value) {
    return navigateTo('/alerts')
  }
})
