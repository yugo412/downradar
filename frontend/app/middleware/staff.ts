// Client side only, and staff only: admins and moderators look after the
// catalogue and the accounts that watch it. The server half is in
// session-guard.ts.
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
    return navigateTo('/')
  }
})
