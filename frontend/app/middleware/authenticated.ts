// Client side only. The server check lives in server/middleware/admin-guard.ts,
// which is the one that can read the session cookie.
export default defineNuxtRouteMiddleware(async (to) => {
  if (import.meta.server) return

  const { user, loaded, load } = useAuth()

  if (!loaded.value) {
    await load()
  }

  if (!user.value) {
    return navigateTo(`/login?next=${encodeURIComponent(to.fullPath)}`)
  }
})
