// Client side only. The server has already been checked by
// server/middleware/admin-guard.ts, which is the one that can read the cookie.
export default defineNuxtRouteMiddleware(async (to) => {
  if (import.meta.server) return

  const { user, loaded, load } = useAuth()

  if (!loaded.value) {
    await load()
  }

  if (!user.value) {
    return navigateTo(`/login?next=${encodeURIComponent(to.fullPath)}`)
  }
  if (user.value.role !== 'admin' && user.value.role !== 'moderator') {
    return navigateTo('/')
  }
})
