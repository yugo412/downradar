import type { AuthProvider } from '~/types/api'

// The ways in that this deployment has configured. The API is the only one that
// knows: a provider without credentials is not in its list, and the screens offer
// what the list carries rather than a button that fails when it is pressed.
export function useAuthProviders() {
  const providers = useState<AuthProvider[]>('auth:providers', () => [])

  async function load() {
    try {
      const res = await $fetch<{ providers: AuthProvider[] }>('/api/v1/auth/providers')
      providers.value = res.providers ?? []
    } catch {
      // A site with no provider sign in is the normal case, not an error worth
      // showing on the form.
      providers.value = []
    }
  }

  // The link the browser follows to start. It is a plain navigation rather than
  // a fetch: the API answers with a redirect to the provider, and the provider
  // comes back to the callback with a session cookie.
  function signInURL(name: string, next = '') {
    const query = next && next !== '/' ? `?next=${encodeURIComponent(next)}` : ''
    return `/api/v1/auth/${name}${query}`
  }

  return { providers, load, signInURL }
}
