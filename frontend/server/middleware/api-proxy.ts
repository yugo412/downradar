export default defineEventHandler((event) => {
  if (!event.path.startsWith('/api/v1/')) return

  const config = useRuntimeConfig()
  const pathname = event.path.split('?')[0]

  // MCP asks the client to accept JSON and SSE even when the server answers
  // JSON. The API's other routes take a plain JSON Accept, but the MCP endpoint
  // refuses that, so it needs both here rather than the override below.
  const accept = pathname === '/api/v1/mcp'
    ? 'application/json, text/event-stream'
    : 'application/json'

  // The client IP is taken from the header Caddy sets, and passed through
  // explicitly: proxyRequest replaces the forwarded headers rather than
  // merging them, so without this the API would only ever see 127.0.0.1 and
  // every visitor would share one rate limit bucket.
  const forwardedFor = getRequestHeader(event, 'x-forwarded-for')

  // Host cannot be set on a fetch request, it is a forbidden header name, so
  // the host the visitor actually used travels in X-Forwarded-Host instead.
  // Without it the API would compare the browser's Origin header against its
  // own address and refuse every write.
  const host = getRequestHeader(event, 'host')

  return proxyRequest(event, `${config.apiBase}${event.path}`, {
    headers: {
      'x-internal-token': config.internalToken,
      accept,
      ...(host ? { 'x-forwarded-host': host } : {}),
      ...(forwardedFor ? { 'x-forwarded-for': forwardedFor } : {}),
    },
    // A sign in through a provider answers with a redirect that the browser has
    // to follow itself: it goes to the provider, and later back to the site with
    // the session cookie. Following it here would fetch somebody else's page on
    // the server and leave the address bar on the API's own path.
    fetchOptions: { redirect: 'manual' },
  })
})
