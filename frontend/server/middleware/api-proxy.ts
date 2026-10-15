export default defineEventHandler((event) => {
  if (!event.path.startsWith('/api/v1/')) return

  const config = useRuntimeConfig()
  return proxyRequest(event, `${config.apiBase}${event.path}`, {
    headers: {
      'x-internal-token': config.internalToken,
      accept: 'application/json',
    },
  })
})
