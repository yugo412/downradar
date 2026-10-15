<script setup lang="ts">
const { siteName, githubRepo } = useRuntimeConfig().public

useTheme()
</script>

<template>
  <div class="min-h-screen flex flex-col bg-base-200">
    <header class="border-b border-base-300 bg-base-100">
      <div class="mx-auto max-w-6xl px-4 py-3 flex items-center justify-between gap-4">
        <NuxtLink to="/" class="flex flex-col min-w-0 text-lg">
          <span class="flex items-center gap-2.5 min-w-0">
            <span class="inline-block size-[0.72em] shrink-0 rounded-full bg-error animate-pulse" />
            <span class="font-semibold tracking-tight">{{ siteName }}</span>
          </span>
          <span class="flex items-center gap-2.5 min-w-0">
            <span class="inline-block size-[0.72em] shrink-0" aria-hidden="true" />
            <span class="text-xs opacity-60 truncate">Official status pages, aggregated</span>
          </span>
        </NuxtLink>
        <nav class="flex items-center gap-1 shrink-0">
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
