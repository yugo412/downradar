<script setup lang="ts">
// One chip of a filter bar: what it narrows a list to, and how many rows are in
// that set. A chip that is off wears the colour it was given, so a row of them
// says which states exist and what they look like. One that is on gives that
// colour up for a dimmed fill, which nothing else on the page uses: an outline in
// the same colour was the first try, but at badge size it has no contrast against
// the page. The tick says "picked" a second time, so it does not rest on
// lightness alone.
defineProps<{ label: string; count: number; cls: string }>()
const picked = defineModel<boolean>({ default: false })
</script>

<template>
  <button
    type="button"
    class="badge badge-sm cursor-pointer whitespace-nowrap transition-colors"
    :class="picked ? 'badge-picked' : cls"
    :aria-pressed="picked"
    @click="picked = !picked"
  >
    <span v-if="picked" aria-hidden="true">✓</span>
    {{ label }} ({{ count }})
  </button>
</template>
