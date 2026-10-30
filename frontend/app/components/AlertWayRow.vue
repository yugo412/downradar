<script setup lang="ts">
type Channel = 'email' | 'webhook' | 'telegram' | 'slack' | 'discord' | 'lark' | 'ntfy' | 'rss'

defineProps<{
  label: string
  icon: Channel
  active: boolean
  on: boolean
  paused: boolean
  busy?: boolean
}>()

const emit = defineEmits<{
  select: []
  pause: [event: Event]
}>()
</script>

<template>
  <li
    class="flex items-center gap-1 rounded-btn"
    :class="active ? 'bg-base-200 font-medium' : 'hover:bg-base-200'"
  >
    <button
      type="button"
      class="flex min-w-0 flex-1 items-center gap-2 rounded-btn px-3 py-2 text-left text-sm"
      :aria-current="active ? 'true' : undefined"
      @click="emit('select')"
    >
      <ChannelIcon :kind="icon" />
      <span class="min-w-0">{{ label }}</span>
    </button>
    <label
      class="flex shrink-0 items-center px-2 py-2"
      :class="on ? 'cursor-pointer' : 'cursor-not-allowed opacity-40'"
    >
      <input
        type="checkbox"
        class="toggle toggle-warning toggle-sm"
        :checked="on && !paused"
        :disabled="!on || busy"
        :aria-label="!on ? `${label} is not set up` : paused ? `Start ${label}` : `Pause ${label}`"
        @change="emit('pause', $event)"
      >
    </label>
  </li>
</template>
