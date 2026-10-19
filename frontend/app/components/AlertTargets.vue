<script setup lang="ts">
import type { ComponentStatus } from '~/types/api'

// What an alert watches: the vendor's own status, the components of it, or both.
// The bell's dialog and the alert page show the same list, so it lives here once
// rather than being kept in step in two places.
//
// A status page can report hundreds of components, so the list is searchable and
// has a scroll box of its own; the search runs in the browser, because the list
// is already on the page and filtering it costs no request.
const props = defineProps<{
  slug: string
  name: string
  status: string
  components: ComponentStatus[]
  // A component is a subscriber's: a visitor without an account gets one alert,
  // and that is a whole service, so the boxes are there to be seen rather than
  // ticked.
  guest?: boolean
  // Whether the list is the last thing in a flex column that has a height of its
  // own, which is how the dialog sizes it, or a box with a cap, which is how a
  // page does.
  fill?: boolean
}>()

const whole = defineModel<boolean>('whole', { required: true })
const picked = defineModel<string[]>('picked', { required: true })

const search = ref('')

const needle = computed(() => search.value.trim().toLowerCase())
const matched = computed(() => {
  if (!needle.value) return props.components
  return props.components.filter(
    (c) =>
      c.name.toLowerCase().includes(needle.value) ||
      (c.group ?? '').toLowerCase().includes(needle.value),
  )
})

// The statuses the list can be narrowed to, counted after the search so that the
// number on a chip always agrees with the rows under it.
const statusFilter = ref<string[]>([])
const statusOptions = computed(() =>
  statusCounts(matched.value, (c) => c.status, statusFilter.value),
)
const shownComponents = computed(() => {
  if (!statusFilter.value.length) return matched.value
  return matched.value.filter((c) => statusFilter.value.includes(c.status))
})
</script>

<template>
  <div class="flex min-h-0 flex-col">
    <div class="px-5 pt-4">
      <input
        v-model="search"
        type="search"
        aria-label="Search components"
        placeholder="Search components"
        class="input input-bordered w-full"
      >

      <div class="mt-3">
        <StatusFilter
          v-model="statusFilter"
          :statuses="statusOptions"
          label="Filter components by status"
        />
      </div>
    </div>

    <div
      class="mt-1 min-h-0 overflow-y-auto border-t border-base-300"
      :class="props.fill ? 'flex-1' : 'max-h-80'"
    >
      <label class="flex cursor-pointer items-center gap-3 px-5 py-2 hover:bg-base-200">
        <input v-model="whole" type="checkbox" class="checkbox checkbox-sm">
        <span class="flex-1 text-sm font-medium">The whole service</span>
        <StatusBadge :status="status" size="xs" />
      </label>

      <p v-if="guest" class="px-5 py-2 text-sm opacity-80">
        Components are for
        <NuxtLink :to="`/login?next=/${slug}`" class="link">subscribers</NuxtLink>.
        Sign in to follow one, or watch the whole service from here.
      </p>

      <label
        v-for="component in shownComponents"
        :key="component.sourceId"
        class="flex items-center gap-3 px-5 py-2"
        :class="guest ? 'opacity-60' : 'cursor-pointer hover:bg-base-200'"
      >
        <input
          v-model="picked"
          type="checkbox"
          class="checkbox checkbox-sm"
          :value="component.sourceId"
          :disabled="guest"
        >
        <span class="min-w-0 flex-1 text-sm">
          {{ component.name }}
          <span v-if="component.group" class="opacity-60">· {{ component.group }}</span>
        </span>
        <StatusBadge :status="component.status" size="xs" />
      </label>

      <p v-if="!shownComponents.length" class="px-5 py-4 text-sm opacity-70">
        <template v-if="!components.length">This service reports no components yet.</template>
        <template v-else-if="!matched.length">
          No component matches “{{ search.trim() }}”.
        </template>
        <template v-else>No component is in the states you picked.</template>
      </p>
    </div>
  </div>
</template>
