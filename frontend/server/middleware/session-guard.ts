// Guards the pages that need a session, on the server, before anything renders.
//
// The client route middleware cannot do this job: it runs before the browser has
// asked the API who the visitor is, and useRequestHeaders does not carry the
// session cookie there. Reading the h3 event directly does work, and failing
// closed is the right behaviour when the API cannot be reached.
const MANAGE_PREFIX = '/services'
const ACCOUNT_PREFIX = '/account'

function under(path: string, prefix: string) {
  return path === prefix || path.startsWith(`${prefix}/`)
}

// Everything under these two prefixes needs a session, and nothing more: the
// catalogue is for staff, but /services is also where anybody sees the private
// services they own. Which rows a person may touch is decided by the API.
const PROTECTED = [MANAGE_PREFIX, ACCOUNT_PREFIX]

export default defineEventHandler(async (event) => {
  const path = event.path.split('?')[0]
  if (!PROTECTED.some((prefix) => under(path, prefix))) return

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
})
