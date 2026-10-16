<script setup lang="ts">
import type { AdminService } from '~/types/api'

defineProps<{
  service: AdminService
  canRemove: boolean
  retrying: boolean
}>()

const emit = defineEmits<{
  edit: []
  publish: []
  retry: []
  restore: []
  remove: []
}>()
</script>

<template>
  <div class="flex flex-wrap items-center gap-2 text-xs">
    <button type="button" class="link" @click="emit('edit')">Edit</button>

    <button
      v-if="service.active && service.status === 'no_match'"
      type="button"
      class="link inline-flex items-center gap-1"
      :disabled="retrying"
      title="Run the profiles again against this page, for when a new one has been deployed."
      @click="emit('retry')"
    >
      <span v-if="retrying" class="loading loading-spinner loading-xs" />
      Retry
    </button>

    <button
      v-if="service.active && service.status === 'proposed'"
      type="button"
      class="link"
      @click="emit('publish')"
    >
      Publish
    </button>

    <button v-if="!service.active" type="button" class="link" @click="emit('restore')">
      Restore
    </button>

    <button
      v-if="service.active"
      type="button"
      class="link text-error"
      :disabled="!canRemove"
      :title="canRemove ? '' : 'Only an admin can remove a published service'"
      @click="emit('remove')"
    >
      Delete
    </button>
  </div>
</template>
