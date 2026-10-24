<script setup lang="ts">
type Channel = 'email' | 'webhook' | 'telegram' | 'slack' | 'discord' | 'lark' | 'rss'

defineProps<{ kind: Channel }>()
</script>

<template>
  <svg
    v-if="kind === 'slack'"
    class="size-4 shrink-0"
    viewBox="0 0 122.8 122.8"
    fill="currentColor"
    aria-hidden="true"
  >
    <path d="M25.8 77.6c0 7.1-5.8 12.9-12.9 12.9S0 84.7 0 77.6s5.8-12.9 12.9-12.9h12.9v12.9Z" />
    <path d="M32.3 77.6c0-7.1 5.8-12.9 12.9-12.9s12.9 5.8 12.9 12.9v32.3c0 7.1-5.8 12.9-12.9 12.9s-12.9-5.8-12.9-12.9V77.6Z" />
    <path d="M45.2 25.8c-7.1 0-12.9-5.8-12.9-12.9S38.1 0 45.2 0s12.9 5.8 12.9 12.9v12.9H45.2Z" />
    <path d="M45.2 32.3c7.1 0 12.9 5.8 12.9 12.9s-5.8 12.9-12.9 12.9H12.9C5.8 58.1 0 52.3 0 45.2s5.8-12.9 12.9-12.9h32.3Z" />
    <path d="M97 45.2c0-7.1 5.8-12.9 12.9-12.9s12.9 5.8 12.9 12.9-5.8 12.9-12.9 12.9H97V45.2Z" />
    <path d="M90.5 45.2c0 7.1-5.8 12.9-12.9 12.9s-12.9-5.8-12.9-12.9V12.9C64.7 5.8 70.5 0 77.6 0s12.9 5.8 12.9 12.9v32.3Z" />
    <path d="M77.6 97c7.1 0 12.9 5.8 12.9 12.9s-5.8 12.9-12.9 12.9-12.9-5.8-12.9-12.9V97h12.9Z" />
    <path d="M77.6 90.5c-7.1 0-12.9-5.8-12.9-12.9s5.8-12.9 12.9-12.9h32.3c7.1 0 12.9 5.8 12.9 12.9s-5.8 12.9-12.9 12.9H77.6Z" />
  </svg>
  <svg
    v-else
    class="size-4 shrink-0"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    stroke-width="2"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
  >
    <template v-if="kind === 'email'">
      <rect x="2" y="4" width="20" height="16" rx="2" />
      <path d="m22 7-8.97 5.7a1.94 1.94 0 0 1-2.06 0L2 7" />
    </template>
    <template v-else-if="kind === 'webhook'">
      <path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71" />
      <path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71" />
    </template>
    <template v-else-if="kind === 'telegram'">
      <path d="m22 2-7 20-4-9-9-4Z" />
      <path d="M22 2 11 13" />
    </template>
    <template v-else-if="kind === 'discord'">
      <path d="M17.32 5H6.68a4 4 0 0 0-3.98 3.59L2 16.5a2.5 2.5 0 0 0 4.4 1.6L8 16h8l1.6 2.1a2.5 2.5 0 0 0 4.4-1.6l-.7-7.91A4 4 0 0 0 17.32 5Z" />
      <path d="M6 12h4M8 10v4M15 11h.01M18 13h.01" />
    </template>
    <template v-else-if="kind === 'lark'">
      <path d="M4 15c4.6 0 7.7-2.6 9.2-7.3L14.5 4l1.1 3.7 4.4.8-3.3 2.7.7 4.2-4.1-2.2C11 16 7.4 17 4 17Z" />
      <circle cx="16.5" cy="8.5" r=".75" fill="currentColor" stroke="none" />
    </template>
    <template v-else-if="kind === 'rss'">
      <path d="M4 11a9 9 0 0 1 9 9" />
      <path d="M4 4a16 16 0 0 1 16 16" />
      <circle cx="5" cy="19" r="1" fill="currentColor" stroke="none" />
    </template>
  </svg>
</template>
