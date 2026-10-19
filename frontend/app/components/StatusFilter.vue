<script setup lang="ts">
import type { StatusCount } from '~/utils/format'

// A row of chips to narrow a list with: each one carries how many rows are in
// that state, and several can be picked at once. It is a toggle group rather
// than a set of checkboxes, because a list of them reads as a filter bar.
//
// clearable says the page has more to put back than these chips — a sort, or a
// picker of its own — so the Clear link is offered even with nothing picked, and
// clearing a chip also tells the page to drop the rest. A page with a chip that
// is not a health state, such as the index's Subscribed, passes it through the
// default slot so it stands beside the states instead of in a row of its own.
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
    class="flex flex-wrap items-center gap-2"
    role="group"
    :aria-label="props.label"
  >
    <FilterChip
      v-for="option in props.statuses"
      :key="option.status"
      :label="statusMeta(option.status).label"
      :count="option.count"
      :cls="statusMeta(option.status).cls"
      :model-value="picked.includes(option.status)"
      @update:model-value="toggle(option.status)"
    />

    <slot />

    <button v-if="picked.length || props.clearable" type="button" class="link text-xs" @click="clear">
      Clear
    </button>
  </div>
</template>
