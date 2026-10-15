export type Theme = 'light' | 'dark'

export function useTheme() {
  const cookie = useCookie<Theme>('theme', {
    default: () => 'light',
    maxAge: 60 * 60 * 24 * 365,
    sameSite: 'lax',
    path: '/',
  })

  const theme = computed<Theme>(() => (cookie.value === 'dark' ? 'dark' : 'light'))

  useHead({
    htmlAttrs: { 'data-theme': theme },
  })

  function setTheme(next: Theme) {
    cookie.value = next
  }

  function toggleTheme() {
    setTheme(theme.value === 'dark' ? 'light' : 'dark')
  }

  return { theme, setTheme, toggleTheme }
}
