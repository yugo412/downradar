<script setup lang="ts">
import type { NuxtError } from '#app'

const props = defineProps<{ error: NuxtError }>()

const { siteName, githubRepo } = useRuntimeConfig().public

useTheme()

const statusCode = computed(() => Number(props.error?.statusCode ?? 500))
const isNotFound = computed(() => statusCode.value === 404)

const heading = computed(() =>
  isNotFound.value ? 'That page is not here' : 'Something broke on our side',
)

// Only our own wording is rendered. Nuxt still serialises the error into the
// page payload, where production keeps the status message but replaces the
// message itself with "Server Error".
const detail = computed(() =>
  isNotFound.value
    ? 'The link may be out of date, or the service was removed. The status board lists everything that is monitored.'
    : 'This page could not be rendered. It is usually a hiccup, and trying again normally works.',
)

useSeoMeta({
  title: isNotFound.value ? 'Page not found' : 'Something went wrong',
  robots: 'noindex, nofollow',
})
</script>

<template>
  <div class="min-h-screen flex flex-col bg-base-200">
    <header class="border-b border-base-300 bg-base-100">
      <div class="mx-auto max-w-6xl px-4 py-3 flex items-center justify-between gap-4">
        <SiteBrand />
        <nav class="flex items-center gap-1 shrink-0">
          <!-- The theme button is drawn by the browser, for the same reason it
               is in the ClientOnly block of app.vue: the server cannot know
               the system's colour scheme. -->
          <ClientOnly>
            <ThemeToggle />
          </ClientOnly>
        </nav>
      </div>
    </header>

    <main class="flex-1">
      <div class="mx-auto max-w-xl px-4 py-16">
        <div class="rounded-box border border-base-300 bg-base-100 p-6 text-center shadow-sm">
          <p class="text-5xl font-bold tracking-tight opacity-25">{{ statusCode }}</p>
          <h1 class="mt-3 text-xl font-semibold tracking-tight">{{ heading }}</h1>
          <p class="mt-2 text-sm opacity-70">{{ detail }}</p>

          <div class="mt-6 flex flex-wrap items-center justify-center gap-2">
            <NuxtLink to="/" class="btn btn-primary btn-sm">Go to the status board</NuxtLink>
            <button
              v-if="!isNotFound"
              type="button"
              class="btn btn-ghost btn-sm"
              @click="clearError()"
            >
              Try again
            </button>
          </div>
        </div>
      </div>
    </main>

    <footer class="border-t border-base-300 bg-base-100">
      <div class="mx-auto max-w-6xl px-4 py-6 text-xs opacity-60">
        <p>
          Status data is taken directly from each service's official status page.
          All names and trademarks belong to their respective owners.
        </p>
        <p class="mt-1 flex flex-wrap items-center gap-x-2">
          <NuxtLink to="/" class="link">{{ siteName }}</NuxtLink>
          <span aria-hidden="true">&middot;</span>
          <NuxtLink to="/api" class="link">Public API</NuxtLink>
          <template v-if="githubRepo">
            <span aria-hidden="true">&middot;</span>
            <a
              :href="`https://github.com/${githubRepo}`"
              target="_blank"
              rel="noopener noreferrer"
              class="link"
            >
              {{ githubRepo }}
            </a>
          </template>
        </p>
      </div>
    </footer>
  </div>
</template>
