<script setup lang="ts">
import type { StatusCount } from '~/utils/format'

// A row of state badges to narrow a list with: each one carries how many rows are
// in that state, and several can be picked at once. It is a toggle group rather
// than a set of checkboxes, because a list of them reads as a filter bar.
const props = defineProps<{ statuses: StatusCount[]; label: string }>()
const picked = defineModel<string[]>({ default: () => [] })

function toggle(status: string) {
  picked.value = picked.value.includes(status)
    ? picked.value.filter((s) => s !== status)
    : [...picked.value, status]
}

// A chip that is off wears the colour of its state, so the row says which states
// exist and what they look like. One that is on gives that colour up for a dimmed
// fill, which nothing else on the page uses: an outline in the state's colour was
// the first try, but at badge size it has no contrast against the page. The tick
// says "picked" a second time, so it does not rest on lightness alone.
</script>

<template>
  <div
    v-if="props.statuses.length"
    class="flex flex-wrap items-center gap-2"
    role="group"
    :aria-label="props.label"
  >
    <button
      v-for="option in props.statuses"
      :key="option.status"
      type="button"
      class="badge badge-sm cursor-pointer whitespace-nowrap transition-colors"
      :class="
        picked.includes(option.status) ? 'badge-picked' : statusMeta(option.status).cls
      "
      :aria-pressed="picked.includes(option.status)"
      @click="toggle(option.status)"
    >
      <span v-if="picked.includes(option.status)" aria-hidden="true">✓</span>
      {{ statusMeta(option.status).label }} ({{ option.count }})
    </button>

    <button v-if="picked.length" type="button" class="link text-xs" @click="picked = []">
      Clear
    </button>
  </div>
</template>
