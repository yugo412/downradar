// Guards the pages that need a session, on the server, before anything renders.
//
// The client route middleware cannot do this job: it runs before the browser has
// asked the API who the visitor is, and useRequestHeaders does not carry the
// session cookie there. Reading the h3 event directly does work, and failing
// closed is the right behaviour when the API cannot be reached.
const SESSION_PREFIXES = ['/account']
const STAFF_PREFIX = '/services'
const ADMIN_PREFIX = '/users'

const STAFF_ROLES = ['admin', 'moderator']

function under(path: string, prefix: string) {
  return path === prefix || path.startsWith(`${prefix}/`)
}

export default defineEventHandler(async (event) => {
  const path = event.path.split('?')[0]
  const needsSession = SESSION_PREFIXES.some((prefix) => under(path, prefix))
  const needsStaff = under(path, STAFF_PREFIX)
  const needsAdmin = under(path, ADMIN_PREFIX)
  if (!needsSession && !needsStaff && !needsAdmin) return

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
  // The service pages belong to the people who run the catalogue; a subscriber
  // is sent to the alerts they look after. /users is admin only.
  if (needsStaff && !STAFF_ROLES.includes(role)) {
    return sendRedirect(event, '/alerts', 302)
  }
  if (needsAdmin && role !== 'admin') {
    return sendRedirect(event, '/', 302)
  }
})
