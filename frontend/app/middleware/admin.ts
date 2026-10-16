// Client side only, and admin only: a moderator looks after the service
// catalogue, not after people. The server half is in session-guard.ts.
export default defineNuxtRouteMiddleware(async (to) => {
  if (import.meta.server) return

  const { user, loaded, load } = useAuth()

  if (!loaded.value) {
    await load()
  }

  if (!user.value) {
    return navigateTo(`/login?next=${encodeURIComponent(to.fullPath)}`)
  }
  if (user.value.role !== 'admin') {
    return navigateTo('/')
  }
})
