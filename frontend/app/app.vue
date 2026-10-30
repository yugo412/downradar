<script setup lang="ts">
const { githubRepo, siteName, siteUrl } = useRuntimeConfig().public
const { user, isAdmin, canManageServices, signOut } = useAuth()
const { unread, loadUnread, setUnread } = useNotifications()

const siteHost = siteUrl.replace(/^https?:\/\//, '').replace(/\/$/, '')

// Every page starts with the site card. A vendor page replaces it with the
// vendor's own, while the pages that are not a vendor keep one shared image.
defineOgImage(
  'Card.takumi',
  {
    eyebrow: 'Official status pages, aggregated',
    name: siteName,
    description:
      'Know when the services you rely on go down, with status read from each official status page.',
    address: siteHost,
  },
  { alt: `${siteName}, official status pages aggregated` },
)

const route = useRoute()

// Search engines treat the index's filter variants as separate pages unless the
// clean address says which one to keep. The canonical is built from the site's
// own address rather than this request, so it is absolute and never carries the
// query string.
const canonical = computed(() => {
  const path = route.path === '/' ? '/' : route.path.replace(/\/+$/, '')
  return `${siteUrl.replace(/\/+$/, '')}${path}`
})

useHead(() => ({
  link: [{ rel: 'canonical', href: canonical.value }],
}))

const openMenu = ref<'service' | 'tools' | 'account' | 'nav' | null>(null)

useTheme()

// The session is loaded in the browser, so the badge follows the same state the
// menus do. A signed out visitor leaves no count behind.
watch(
  user,
  (value) => {
    if (value) {
      if (import.meta.client) void loadUnread()
      return
    }
    setUnread(0)
  },
  { immediate: true },
)

watch(
  () => route.fullPath,
  () => {
    openMenu.value = null
  },
)

function toggle(menu: 'service' | 'tools' | 'account' | 'nav') {
  openMenu.value = openMenu.value === menu ? null : menu
}

// One list of destinations, rendered twice: as dropdowns on a wide screen and as
// a single panel on a narrow one. Written once so the two cannot drift apart.
const serviceLinks = [
  { label: 'Add service', to: '/services/new' },
  { label: 'List services', to: '/services' },
  { label: 'All alerts', to: '/alerts/all' },
]

const toolLinks = [{ label: 'Broadcast notification', to: '/tools' }]

const accountLinks = [
  { label: 'Edit profile', to: '/account/profile' },
  { label: 'Change password', to: '/account/password' },
  { label: 'Linked logins', to: '/account/logins' },
]

async function onSignOut() {
  openMenu.value = null
  await signOut()
  await navigateTo('/')
}

const resending = ref(false)
const resent = ref(false)

// The banner above every page, for a signed in visitor whose address has not
// been confirmed. Nothing is blocked until it is: the link may have gone astray,
// and being unable to do anything until an email arrives would be worse.
async function resend() {
  resending.value = true
  resent.value = false

  try {
    await $fetch('/api/v1/auth/verify/resend', { method: 'POST' })
    resent.value = true
  } catch {
    // Nothing to say: the banner is not the place for an error, and the button
    // is the same click again.
  } finally {
    resending.value = false
  }
}
</script>

<template>
  <div class="min-h-screen flex flex-col bg-base-200">
    <header class="relative border-b border-base-300 bg-base-100">
      <div class="mx-auto max-w-6xl px-4 py-3 flex items-center justify-between gap-4">
        <SiteBrand />
        <nav class="flex items-center gap-1 shrink-0">
          <!-- ClientOnly is required, not cosmetic: the session is fetched in the
               browser, so rendering this from the server would either leak one
               visitor's session into the cached page or break hydration. The
               theme switch is in here for the same kind of reason: the server
               cannot know the system's colour scheme, and the page it renders
               is cached and handed to every visitor, so a switch rendered on
               the server would be the one of whoever the cached page was
               rendered for. -->
          <ClientOnly>
            <!-- The first control is the theme switch, so it is the first thing
                 a visitor meets in the header. -->
            <ThemeToggle />

            <!-- These stand on their own rather than under the account menu,
                 and are hidden on a narrow screen: there the copies inside the
                 panel below are the ones that show. Alerts and Services are
                 shown to a visitor without an account too, because both pages
                 are what the site is for; the pages send them to sign in, and
                 the address they asked for is kept for afterwards. -->
            <NuxtLink
              v-if="isAdmin"
              to="/users"
              class="btn btn-ghost btn-sm hidden md:inline-flex"
            >
              Users
            </NuxtLink>

            <NuxtLink to="/alerts" class="btn btn-ghost btn-sm hidden md:inline-flex">
              Alerts
            </NuxtLink>

            <!-- A subscriber looks after the services they added from the same
                 list, which is the menu entry the staff reach through Service. -->
            <NuxtLink
              v-if="!canManageServices"
              to="/services"
              class="btn btn-ghost btn-sm hidden md:inline-flex"
            >
              Services
            </NuxtLink>

            <NuxtLink to="/contact" class="btn btn-ghost btn-sm hidden md:inline-flex">
              Contact
            </NuxtLink>

            <!-- A visitor without an account gets these instead of the menus
                 below, and they are hidden on a narrow screen for the same
                 reason: the copies in the panel are the ones that show. -->
            <NuxtLink
              v-if="!user"
              to="/login"
              class="btn btn-ghost btn-sm hidden md:inline-flex"
            >
              Sign in
            </NuxtLink>
            <NuxtLink
              v-if="!user"
              to="/register"
              class="btn btn-primary btn-sm hidden md:inline-flex"
            >
              Create account
            </NuxtLink>

            <!-- One menu per concern. Service is where the people who run the
                 catalogue manage it, and the account menu belongs to whoever is
                 signed in. -->
            <div v-if="user" class="hidden items-center gap-1 md:flex">
              <div v-if="canManageServices" class="relative">
                <button
                  type="button"
                  class="btn btn-ghost btn-sm"
                  :aria-expanded="openMenu === 'service'"
                  @click="toggle('service')"
                >
                  Service
                  <svg class="h-3 w-3 opacity-60" viewBox="0 0 12 12" fill="currentColor" aria-hidden="true">
                    <path d="M6 8.5 1.5 4h9z" />
                  </svg>
                </button>
                <ul
                  v-if="openMenu === 'service'"
                  class="absolute right-0 z-50 mt-1 w-60 rounded-box border border-base-300 bg-base-100 p-2 shadow-lg"
                >
                  <li v-for="l in serviceLinks" :key="l.to">
                    <NuxtLink :to="l.to" class="block rounded-btn px-3 py-2 text-sm hover:bg-base-200">
                      {{ l.label }}
                    </NuxtLink>
                  </li>
                </ul>
              </div>

              <div v-if="isAdmin" class="relative">
                <button
                  type="button"
                  class="btn btn-ghost btn-sm"
                  :aria-expanded="openMenu === 'tools'"
                  @click="toggle('tools')"
                >
                  Tools
                  <svg class="h-3 w-3 opacity-60" viewBox="0 0 12 12" fill="currentColor" aria-hidden="true">
                    <path d="M6 8.5 1.5 4h9z" />
                  </svg>
                </button>
                <ul
                  v-if="openMenu === 'tools'"
                  class="absolute right-0 z-50 mt-1 w-60 rounded-box border border-base-300 bg-base-100 p-2 shadow-lg"
                >
                  <li v-for="l in toolLinks" :key="l.to">
                    <NuxtLink :to="l.to" class="block rounded-btn px-3 py-2 text-sm hover:bg-base-200">
                      {{ l.label }}
                    </NuxtLink>
                  </li>
                </ul>
              </div>

              <div class="relative">
                <button
                  type="button"
                  class="btn btn-ghost btn-sm"
                  :aria-expanded="openMenu === 'account'"
                  @click="toggle('account')"
                >
                  {{ user.name }}
                  <svg class="h-3 w-3 opacity-60" viewBox="0 0 12 12" fill="currentColor" aria-hidden="true">
                    <path d="M6 8.5 1.5 4h9z" />
                  </svg>
                </button>
                <ul
                  v-if="openMenu === 'account'"
                  class="absolute right-0 z-50 mt-1 w-60 rounded-box border border-base-300 bg-base-100 p-2 shadow-lg"
                >
                  <li class="px-3 pb-2 pt-1 text-xs opacity-60">
                    {{ user.email }} ({{ user.role }})
                  </li>
                  <li v-for="l in accountLinks" :key="l.to">
                    <NuxtLink :to="l.to" class="block rounded-btn px-3 py-2 text-sm hover:bg-base-200">
                      {{ l.label }}
                    </NuxtLink>
                  </li>
                  <li class="mt-1 border-t border-base-300 pt-1">
                    <button
                      type="button"
                      class="block w-full rounded-btn px-3 py-2 text-left text-sm hover:bg-base-200"
                      @click="onSignOut"
                    >
                      Sign out
                    </button>
                  </li>
                </ul>
              </div>
            </div>

            <!-- The bell is the only entry to the feed. It sits after the
                 account menu and carries the unread count, so the feed itself
                 stays a page rather than a popover. -->
            <div v-if="user" class="indicator">
              <span
                v-if="unread > 0"
                class="indicator-item badge badge-primary badge-xs"
                aria-hidden="true"
              >
                {{ unread > 99 ? '99+' : unread }}
              </span>
              <NuxtLink
                to="/notifications"
                class="btn btn-ghost btn-sm btn-circle"
                :aria-label="unread > 0 ? `Notifications, ${unread} unread` : 'Notifications'"
              >
                <BellIcon class="h-5 w-5" />
              </NuxtLink>
            </div>

            <!-- Narrow screens get one panel instead of the dropdowns, for a
                 visitor with an account and one without alike. The panel is a
                 child of the nav but positions against the header, which is why
                 the header carries relative. -->
            <div class="md:hidden">
              <button
                type="button"
                class="btn btn-ghost btn-sm btn-circle"
                :aria-expanded="openMenu === 'nav'"
                aria-label="Menu"
                @click="toggle('nav')"
              >
                <svg
                  class="h-5 w-5"
                  viewBox="0 0 24 24"
                  fill="none"
                  stroke="currentColor"
                  stroke-width="2"
                  aria-hidden="true"
                >
                  <path stroke-linecap="round" d="M4 7h16M4 12h16M4 17h16" />
                </svg>
              </button>
            </div>

            <div v-if="openMenu" class="fixed inset-0 z-40" @click="openMenu = null" />

            <div
              v-if="openMenu === 'nav'"
              class="absolute inset-x-0 top-full z-50 border-b border-base-300 bg-base-100 px-4 pb-4 shadow-lg md:hidden"
            >
              <NuxtLink to="/alerts" class="block rounded-btn px-3 py-2 text-sm hover:bg-base-200">
                Alerts
              </NuxtLink>

              <NuxtLink
                v-if="!canManageServices"
                to="/services"
                class="block rounded-btn px-3 py-2 text-sm hover:bg-base-200"
              >
                Services
              </NuxtLink>

              <NuxtLink
                v-if="isAdmin"
                to="/users"
                class="block rounded-btn px-3 py-2 text-sm hover:bg-base-200"
              >
                Users
              </NuxtLink>

              <NuxtLink to="/contact" class="block rounded-btn px-3 py-2 text-sm hover:bg-base-200">
                Contact
              </NuxtLink>

              <template v-if="canManageServices">
                <p class="px-3 pb-1 pt-3 text-xs uppercase tracking-wide opacity-50">Service</p>
                <NuxtLink
                  v-for="l in serviceLinks"
                  :key="l.to"
                  :to="l.to"
                  class="block rounded-btn px-3 py-2 text-sm hover:bg-base-200"
                >
                  {{ l.label }}
                </NuxtLink>
              </template>

              <template v-if="isAdmin">
                <p class="px-3 pb-1 pt-3 text-xs uppercase tracking-wide opacity-50">Tools</p>
                <NuxtLink
                  v-for="l in toolLinks"
                  :key="l.to"
                  :to="l.to"
                  class="block rounded-btn px-3 py-2 text-sm hover:bg-base-200"
                >
                  {{ l.label }}
                </NuxtLink>
              </template>

              <template v-if="user">
                <div class="my-2 border-t border-base-300" />

                <p class="px-3 pb-1 text-xs uppercase tracking-wide opacity-50">
                  {{ user.name }} ({{ user.role }})
                </p>
                <NuxtLink
                  v-for="l in accountLinks"
                  :key="l.to"
                  :to="l.to"
                  class="block rounded-btn px-3 py-2 text-sm hover:bg-base-200"
                >
                  {{ l.label }}
                </NuxtLink>
                <button
                  type="button"
                  class="block w-full rounded-btn px-3 py-2 text-left text-sm hover:bg-base-200"
                  @click="onSignOut"
                >
                  Sign out
                </button>
              </template>

              <template v-else>
                <div class="my-2 border-t border-base-300" />
                <NuxtLink to="/login" class="block rounded-btn px-3 py-2 text-sm hover:bg-base-200">
                  Sign in
                </NuxtLink>
                <NuxtLink
                  to="/register"
                  class="block rounded-btn px-3 py-2 text-sm hover:bg-base-200"
                >
                  Create account
                </NuxtLink>
              </template>
            </div>
          </ClientOnly>
        </nav>
      </div>
    </header>

    <ClientOnly>
      <div
        v-if="user && !user.verifiedAt"
        class="border-b border-warning/40 bg-warning/10"
      >
        <div class="mx-auto flex max-w-6xl flex-wrap items-center gap-2 px-4 py-2 text-sm">
          <span>Your email address is not confirmed yet. Check your inbox for the link.</span>
          <button
            type="button"
            class="link inline-flex items-center gap-1"
            :disabled="resending"
            @click="resend"
          >
            <span v-if="resending" class="loading loading-spinner loading-xs" />
            Send it again
          </button>
          <span v-if="resent" class="text-success">Sent</span>
        </div>
      </div>
    </ClientOnly>

    <main class="flex-1">
      <NuxtPage />
    </main>

    <footer class="border-t border-base-300 bg-base-100">
      <div class="mx-auto max-w-6xl px-4 py-6 text-xs opacity-60 space-y-2">
        <p>
          Status data is taken directly from each service's official status page.
          All names and trademarks belong to their respective owners.
        </p>
        <p>Not affiliated with any of the services listed.</p>
        <p class="flex flex-wrap items-center gap-x-2 gap-y-1">
          <NuxtLink to="/about" class="link">About</NuxtLink>
          <span aria-hidden="true">&middot;</span>
          <NuxtLink to="/privacy" class="link">Privacy</NuxtLink>
          <span aria-hidden="true">&middot;</span>
          <NuxtLink to="/terms" class="link">Terms</NuxtLink>
          <span aria-hidden="true">&middot;</span>
          <NuxtLink to="/api" class="link">Public API</NuxtLink>
          <span aria-hidden="true">&middot;</span>
          <NuxtLink to="/api#mcp" class="link">MCP server</NuxtLink>
          <span aria-hidden="true">&middot;</span>
          <a href="/sitemap.xml" class="link">Sitemap</a>
          <template v-if="githubRepo">
            <span aria-hidden="true">&middot;</span>
            <a
              :href="`https://github.com/${githubRepo}`"
              target="_blank"
              rel="noopener noreferrer"
              class="link inline-flex items-center gap-1"
            >
              <svg
                class="h-3.5 w-3.5"
                viewBox="0 0 16 16"
                fill="currentColor"
                aria-hidden="true"
              >
                <path
                  d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27s1.36.09 2 .27c1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.01 8.01 0 0 0 16 8c0-4.42-3.58-8-8-8z"
                />
              </svg>
              {{ githubRepo }}
            </a>
          </template>
        </p>
      </div>
    </footer>
  </div>
</template>
