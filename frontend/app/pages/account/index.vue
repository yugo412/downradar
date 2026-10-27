<script setup lang="ts">
definePageMeta({ middleware: 'authenticated', ssr: false })

useSeoMeta({ title: 'Account', robots: 'noindex, nofollow' })

// Nothing lives at /account itself, the menu points at the account screens.
onMounted(() => navigateTo('/account/profile', { replace: true }))
</script>

<template>
  <div class="mx-auto flex max-w-2xl justify-center px-4 py-16">
    <span class="loading loading-spinner" />
  </div>
</template>
