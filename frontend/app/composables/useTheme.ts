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

  const theme = computed<Theme>(() => (cookie.value === 'dark' ? 'dark' : 'cmyk'))

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
