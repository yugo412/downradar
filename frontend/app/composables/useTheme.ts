// The light theme is called `cmyk`, after the daisyUI palette it is built from;
// both themes are set up in app/assets/css/main.css.
export type Theme = 'cmyk' | 'dark'

// The theme is a cookie, and it is applied by the inline script in
// nuxt.config.ts before the first paint.
//
// It is deliberately NOT rendered into the head. Pages are cached for a minute,
// so a server rendered data-theme would be handed to the wrong visitor, and the
// Set-Cookie that came with it would overwrite their choice: that is why dark
// mode kept reverting to light.
//
// The cookie has no default for the same reason. A default makes Nuxt write the
// cookie on every response that did not carry one, and those responses are
// cached too.
export function useTheme() {
  const cookie = useCookie<Theme>('theme', {
    maxAge: 60 * 60 * 24 * 365,
    sameSite: 'lax',
    path: '/',
  })

  // The theme the system asks for. Only the browser knows it, and the server
  // cannot know it at all: the page it renders is cached and handed to every
  // visitor, so a theme read from the request would be the wrong one for
  // most of them. The inline script in nuxt.config.ts reads the same thing
  // before the first paint, which is what puts the right theme on the page.
  const system = import.meta.client
    ? window.matchMedia('(prefers-color-scheme: dark)').matches
      ? 'dark'
      : 'cmyk'
    : null

  // The theme in force: the one the visitor chose, if the cookie carries it,
  // and otherwise the one their system asks for. The cookie is written only
  // when the toggle is used, so a visitor who never touches it keeps
  // following the system.
  const theme = computed<Theme>(() => {
    const chosen = cookie.value
    if (chosen === 'dark' || chosen === 'cmyk') {
      return chosen
    }
    return system ?? 'cmyk'
  })

  function apply(next: Theme) {
    if (import.meta.client) {
      document.documentElement.dataset.theme = next
    }
  }

  function setTheme(next: Theme) {
    cookie.value = next
    apply(next)
  }

  function toggleTheme() {
    setTheme(theme.value === 'dark' ? 'cmyk' : 'dark')
  }

  return { theme, setTheme, toggleTheme }
}
