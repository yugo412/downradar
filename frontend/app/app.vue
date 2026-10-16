<script setup lang="ts">
const { githubRepo } = useRuntimeConfig().public
const { user, signOut } = useAuth()

const route = useRoute()
const openMenu = ref<'service' | 'account' | 'nav' | null>(null)

useTheme()

watch(
  () => route.fullPath,
  () => {
    openMenu.value = null
  },
)

function toggle(menu: 'service' | 'account' | 'nav') {
  openMenu.value = openMenu.value === menu ? null : menu
}

// One list of destinations, rendered twice: as dropdowns on a wide screen and as
// a single panel on a narrow one. Written once so the two cannot drift apart.
const serviceLinks = [
  { label: 'Add service', to: '/services/new' },
  { label: 'List services', to: '/services' },
]

const accountLinks = [
  { label: 'Edit profile', to: '/account/profile' },
  { label: 'Change password', to: '/account/password' },
]

async function onSignOut() {
  openMenu.value = null
  await signOut()
  await navigateTo('/')
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
               visitor's session into the cached page or break hydration. -->
          <ClientOnly>
            <!-- One menu per concern. Service is where a person manages the
                 services they can see, and the account menu belongs to whoever
                 is signed in. -->
            <div v-if="user" class="hidden items-center gap-1 md:flex">
              <div class="relative">
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

            <!-- Narrow screens get one panel instead of three dropdowns. The
                 panel is a child of the nav but positions against the header,
                 which is why the header carries relative. -->
            <div v-if="user" class="md:hidden">
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
              <p class="px-3 pb-1 pt-3 text-xs uppercase tracking-wide opacity-50">Service</p>
              <NuxtLink
                v-for="l in serviceLinks"
                :key="l.to"
                :to="l.to"
                class="block rounded-btn px-3 py-2 text-sm hover:bg-base-200"
              >
                {{ l.label }}
              </NuxtLink>

              <div class="my-2 border-t border-base-300" />

              <p class="px-3 pb-1 text-xs uppercase tracking-wide opacity-50">
                {{ user?.name }} ({{ user?.role }})
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
            </div>
          </ClientOnly>
          <ThemeToggle />
        </nav>
      </div>
    </header>

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
          <NuxtLink to="/api" class="link">Public API</NuxtLink>
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
