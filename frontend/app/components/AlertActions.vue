<script setup lang="ts">
import type { AccountAlert } from '~/types/api'

defineProps<{
  alert: AccountAlert
  busy?: boolean
}>()

const emit = defineEmits<{
  state: [state: AccountAlert['state']]
  remove: []
}>()
</script>

<template>
  <div class="flex flex-wrap items-center gap-1">
    <button
      v-if="alert.state === 'active'"
      type="button"
      class="btn btn-ghost btn-xs"
      :disabled="busy"
      @click="emit('state', 'paused')"
    >
      Pause
    </button>
    <button
      v-else
      type="button"
      class="btn btn-ghost btn-xs"
      :disabled="busy"
      @click="emit('state', 'active')"
    >
      Subscribe
    </button>
    <button
      v-if="alert.state === 'active'"
      type="button"
      class="btn btn-ghost btn-xs"
      :disabled="busy"
      @click="emit('state', 'unsubscribed')"
    >
      Unsubscribe
    </button>
    <button
      type="button"
      class="btn btn-ghost btn-xs text-error"
      :disabled="busy"
      @click="emit('remove')"
    >
      Delete
    </button>
  </div>
</template>
