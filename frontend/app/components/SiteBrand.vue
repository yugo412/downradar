<script setup lang="ts">
// The brand block, shared by the normal layout and the error page, which cannot
// use app.vue because it replaces it.
const { siteName } = useRuntimeConfig().public
</script>

<template>
  <NuxtLink to="/" class="flex flex-col min-w-0 text-lg">
    <span class="flex items-center gap-2.5 min-w-0">
      <span class="inline-block size-[0.72em] shrink-0 rounded-full bg-error animate-pulse" />
      <span class="font-semibold tracking-tight">{{ siteName }}</span>
    </span>
    <span class="flex items-center gap-2.5 min-w-0">
      <span class="inline-block size-[0.72em] shrink-0" aria-hidden="true" />
      <span class="text-xs opacity-60">Official status pages, aggregated</span>
    </span>
  </NuxtLink>
</template>
