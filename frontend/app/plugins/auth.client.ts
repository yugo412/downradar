// Client only, and that is deliberate. Public pages are cached with SWR, so a
// server rendered session would be baked into the cached HTML and handed to the
// next visitor. Asking the API from the browser keeps every session its own.
export default defineNuxtPlugin(async () => {
  const { loaded, load } = useAuth()

  if (loaded.value) return
  await load()
})
