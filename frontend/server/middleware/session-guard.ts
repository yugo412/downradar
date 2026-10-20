// Guards the pages that need a session, on the server, before anything renders.
//
// The client route middleware cannot do this job: it runs before the browser has
// asked the API who the visitor is, and useRequestHeaders does not carry the
// session cookie there. Reading the h3 event directly does work, and failing
// closed is the right behaviour when the API cannot be reached.
const SESSION_PREFIXES = ['/account', '/alerts', '/services']
const ADMIN_PREFIX = '/users'
// The link in an alert email has to work without a session, even though it sits
// under a prefix that otherwise needs one.
const PUBLIC_PATHS = ['/alerts/unsubscribe']

function under(path: string, prefix: string) {
  return path === prefix || path.startsWith(`${prefix}/`)
}

export default defineEventHandler(async (event) => {
  const path = event.path.split('?')[0]
  const needsSession =
    !PUBLIC_PATHS.some((prefix) => under(path, prefix)) &&
    SESSION_PREFIXES.some((prefix) => under(path, prefix))
  const needsAdmin = under(path, ADMIN_PREFIX)
  if (!needsSession && !needsAdmin) return

  const config = useRuntimeConfig()

  let role = ''
  try {
    const res = await $fetch<{ user: { role: string } | null }>(
      `${config.apiBase}/api/v1/auth/me`,
      {
        headers: {
          cookie: getRequestHeader(event, 'cookie') ?? '',
          'x-internal-token': config.internalToken,
        },
      },
    )
    role = res?.user?.role ?? ''
  } catch {
    role = ''
  }

  if (role === '') {
    return sendRedirect(event, `/login?next=${encodeURIComponent(path)}`, 302)
  }
  if (needsAdmin && role !== 'admin') {
    return sendRedirect(event, '/', 302)
  }
})
