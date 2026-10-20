import type { AuthUser } from '~/types/api'

export function useAuth() {
  const user = useState<AuthUser | null>('auth:user', () => null)
  const loaded = useState<boolean>('auth:loaded', () => false)

  const canManageServices = computed(
    () => user.value?.role === 'admin' || user.value?.role === 'moderator',
  )
  const isAdmin = computed(() => user.value?.role === 'admin')

  // Called once per page load by the client plugin, and by the admin route
  // middleware on the client. The session lives in an httpOnly cookie, which the
  // browser attaches on its own.
  async function load() {
    try {
      const res = await $fetch<{ user: AuthUser | null }>('/api/v1/auth/me')
      user.value = res.user
    } catch {
      user.value = null
    } finally {
      loaded.value = true
    }
  }

  // remember is whether the browser should still have the session after it
  // closes; without it the server hands out a cookie that lives that long.
  async function signIn(email: string, password: string, remember = false) {
    const res = await $fetch<{ user: AuthUser }>('/api/v1/auth/login', {
      method: 'POST',
      body: { email, password, remember },
    })
    user.value = res.user
    return res.user
  }

  // The link goes to the address somebody typed, not to an account: the API
  // answers with an error when nobody has it, and sends nothing.
  async function sendSignInLink(email: string, next = '') {
    return $fetch<{ status: string }>('/api/v1/auth/link', {
      method: 'POST',
      body: { email, next },
    })
  }

  // A link from the inbox signs in and confirms the address it was sent to, so
  // the account it belongs to comes back already verified. It keeps the browser
  // signed in: following a link in your own mail is a deliberate act.
  async function signInWithLink(token: string) {
    const res = await $fetch<{ user: AuthUser }>('/api/v1/auth/link/session', {
      method: 'POST',
      body: { token, remember: true },
    })
    user.value = res.user
    loaded.value = true
    return res.user
  }

  // Registering signs the person in as well, which is what the API does, so the
  // session state is set here rather than on the login screen afterwards.
  async function register(name: string, email: string, password: string) {
    const res = await $fetch<{ user: AuthUser }>('/api/v1/auth/register', {
      method: 'POST',
      body: { name, email, password },
    })
    user.value = res.user
    loaded.value = true
    return res.user
  }

  async function signOut() {
    try {
      await $fetch('/api/v1/auth/logout', { method: 'POST' })
    } finally {
      user.value = null
    }
  }

  async function updateProfile(name: string, email: string) {
    const res = await $fetch<{ user: AuthUser }>('/api/v1/auth/profile', {
      method: 'PATCH',
      body: { name, email },
    })
    user.value = res.user
    return res.user
  }

  async function changePassword(currentPassword: string, newPassword: string) {
    return $fetch<{ ok: boolean; otherSessionsEnded: number }>('/api/v1/auth/password', {
      method: 'POST',
      body: { currentPassword, newPassword },
    })
  }

  return {
    user,
    loaded,
    canManageServices,
    isAdmin,
    load,
    signIn,
    sendSignInLink,
    signInWithLink,
    register,
    signOut,
    updateProfile,
    changePassword,
  }
}
