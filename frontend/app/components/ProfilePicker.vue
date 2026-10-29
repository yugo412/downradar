<script setup lang="ts">
import type { AdminProfile } from '~/types/api'

const props = withDefaults(
  defineProps<{
    profiles: AdminProfile[]
    modelValue: string
    // A profile can be selected from another list, such as the default picker
    // showing a single-vendor profile that is not in its own list.
    current?: AdminProfile
    searchLabel?: string
  }>(),
  { current: undefined, searchLabel: 'Search profiles' },
)

const emit = defineEmits<{
  'update:modelValue': [value: string]
}>()

const selected = computed(
  () => props.current ?? props.profiles.find((p) => p.name === props.modelValue),
)

// The names are short and the list can grow, so the search is what keeps two
// profiles with similar names apart.
const search = ref('')
const searchInput = ref<HTMLInputElement | null>(null)
const menu = ref<HTMLDetailsElement | null>(null)

const filtered = computed(() => {
  const query = search.value.trim().toLowerCase()
  if (!query) return props.profiles
  return props.profiles.filter((p) =>
    `${p.name} ${p.kind} ${p.description ?? ''}`.toLowerCase().includes(query),
  )
})

function choose(name: string) {
  emit('update:modelValue', name)
  search.value = ''
  menu.value?.removeAttribute('open')
}

// A fresh search each time the menu opens, and the input in reach without a
// second click.
function onToggle(event: Event) {
  if (!(event.target as HTMLDetailsElement).open) return
  search.value = ''
  nextTick(() => searchInput.value?.focus())
}
</script>

<template>
  <!-- A native select cannot show what each profile reads, and that one line is
       the whole difference between two of them. The list is a dropdown of its
       own for that reason. -->
  <details ref="menu" class="dropdown w-full" @toggle="onToggle">
    <summary
      class="flex cursor-pointer flex-col gap-0.5 rounded-box border border-base-300 bg-base-100 px-3 py-2 text-sm"
      :class="selected ? '' : 'opacity-70'"
    >
      <span class="text-sm">
        {{ selected ? `${selected.name} (${selected.kind})` : 'Not chosen yet' }}
      </span>
      <span v-if="selected?.description" class="text-xs opacity-70">
        {{ selected.description }}
      </span>
    </summary>
    <div
      class="dropdown-content z-20 mt-1 w-full rounded-box border border-base-300 bg-base-100 p-2 shadow"
    >
      <input
        ref="searchInput"
        v-model="search"
        type="search"
        class="input input-bordered mb-2 w-full"
        :placeholder="searchLabel"
        :aria-label="searchLabel"
        @keydown.enter.prevent
        @keydown.esc.prevent="menu?.removeAttribute('open')"
      />
      <ul class="menu max-h-72 w-full flex-nowrap overflow-y-auto p-0">
        <li v-if="!search.trim()">
          <button type="button" class="text-sm" @click="choose('')">Not chosen yet</button>
        </li>
        <li v-for="p in filtered" :key="p.name">
          <button type="button" class="flex flex-col items-start gap-0.5" @click="choose(p.name)">
            <span class="font-mono text-xs">{{ p.name }} ({{ p.kind }})</span>
            <span class="text-xs opacity-70">{{ p.description }}</span>
          </button>
        </li>
      </ul>
      <p v-if="search.trim() && !filtered.length" class="px-2 py-3 text-xs opacity-70">
        No profile matches that search.
      </p>
    </div>
  </details>
</template>
