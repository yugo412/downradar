<script setup lang="ts">
// One chip of a filter row: what it narrows a list to, and how many rows are in
// that set. It is a checkbox wearing a button, which is the shape daisyUI's
// filter is built from, so a chip passed in through the slot and a state chip
// look and behave the same.
//
// The count rides in the label rather than beside it, because a checkbox button
// has no room for a second element: what a reader sees, and what a screen reader
// announces, is the label the input carries.
//
// ink is the state's own colour, worn by the label while the chip is off. A chip
// that is on is painted by daisyUI, which is what says it is picked, and a
// coloured label on that fill would be unreadable.
//
// A chip that is off takes the surface colour rather than daisyUI's base-200,
// which is the colour of the page behind it: bare text would leave the row
// floating beside the search box instead of sitting in it.
const props = defineProps<{ label: string; count: number; ink?: string }>()
const picked = defineModel<boolean>({ default: false })
</script>

<template>
  <input
    type="checkbox"
    class="btn btn-sm"
    :class="picked ? '' : ['border-base-300 bg-base-100', props.ink]"
    :aria-label="`${label} (${count})`"
    :checked="picked"
    @change="picked = !picked"
  >
</template>
