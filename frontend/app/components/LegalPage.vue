<script setup lang="ts">
defineProps<{ title: string; updated: string }>()
</script>

<template>
  <div class="mx-auto max-w-6xl px-4 py-8">
    <article class="rounded-box border border-base-300 bg-base-100 p-5 md:p-8">
      <h1 class="text-3xl font-bold tracking-tight">{{ title }}</h1>
      <div class="mt-6 space-y-8 text-sm leading-relaxed opacity-80">
        <slot />
      </div>
      <p class="mt-10 border-t border-base-300 pt-4 text-xs opacity-60">
        Last updated: {{ updated }}
      </p>
    </article>
  </div>
</template>
