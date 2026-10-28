<script setup lang="ts">
import type { StatusCount } from '~/utils/format'

// A row of chips to narrow a list with, built on daisyUI's filter: a row of
// checkbox buttons, each carrying how many rows are in that state, with a reset
// that the component keeps out of sight until something is picked. Several chips
// can be on at once, which is why it is the checkbox form rather than the radio
// one. The row is a div rather than the form the docs show, because the bell
// already sits in one and a form cannot hold a form.
//
// clearable says the page has more to put back than these chips — a sort, a
// stage, or a picker of its own — so Clear is offered even with nothing picked,
// and clearing a chip also tells the page to drop the rest. A page with a chip
// that is not a health state, such as the index's Subscribed, passes it through
// the default slot so it stands beside the states instead of in a row of its own.
const props = defineProps<{ statuses: StatusCount[]; label: string; clearable?: boolean }>()
const picked = defineModel<string[]>({ default: () => [] })
const slots = useSlots()

const emit = defineEmits<{ clear: [] }>()

function toggle(status: string) {
  picked.value = picked.value.includes(status)
    ? picked.value.filter((s) => s !== status)
    : [...picked.value, status]
}

function clear() {
  picked.value = []
  emit('clear')
}
</script>

<template>
  <div
    v-if="props.statuses.length || props.clearable || slots.default"
    class="filter gap-2"
    role="group"
    :aria-label="props.label"
  >
    <FilterChip
      v-for="option in props.statuses"
      :key="option.status"
      :label="statusMeta(option.status).label"
      :count="option.count"
      :ink="statusMeta(option.status).ink"
      :model-value="picked.includes(option.status)"
      @update:model-value="toggle(option.status)"
    />

    <slot />

    <!-- The reset is daisyUI's own: it appears once a chip is on, and taking it
         empties the row. A page with more to put back keeps a plain Clear for
         the times nothing is picked, since the reset is not there to press. -->
    <button
      v-if="props.clearable && !picked.length"
      type="button"
      class="link self-center text-xs"
      @click="clear"
    >
      Clear
    </button>
    <button
      v-else
      type="button"
      class="btn btn-square btn-sm filter-reset"
      aria-label="Clear"
      @click="clear"
    >
      ×
    </button>
  </div>
</template>
