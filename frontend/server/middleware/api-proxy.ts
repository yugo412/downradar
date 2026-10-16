export default defineEventHandler((event) => {
  if (!event.path.startsWith('/api/v1/')) return

  const config = useRuntimeConfig()

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
      accept: 'application/json',
      ...(host ? { 'x-forwarded-host': host } : {}),
      ...(forwardedFor ? { 'x-forwarded-for': forwardedFor } : {}),
    },
  })
})
