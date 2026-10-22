<script setup lang="ts">
// The mark beside a provider's button. A provider without one is still offered,
// with its label alone, so a new provider does not have to wait for a drawing.
defineProps<{ name: string }>()
</script>

<template>
  <GitHubIcon v-if="name === 'github'" class="size-4 shrink-0" />
  <GoogleIcon v-else-if="name === 'google'" class="size-4 shrink-0" />
</template>
