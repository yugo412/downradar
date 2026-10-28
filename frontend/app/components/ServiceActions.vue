<script setup lang="ts">
import type { AdminService } from '~/types/api'

defineProps<{
  service: AdminService
  canRemove: boolean
  canPurge: boolean
  // Editing a row is the owner's and the staff's. Moving one between stages and
  // running the detection queue are the staff's.
  canEdit: boolean
  canManage: boolean
  retrying: boolean
  pausing: boolean
}>()

const emit = defineEmits<{
  edit: []
  pause: []
  publish: []
  retry: []
  restore: []
  remove: []
  purge: []
}>()
</script>

<template>
  <div class="flex flex-wrap items-center gap-2 text-xs">
    <button v-if="canEdit" type="button" class="link" @click="emit('edit')">Edit</button>

    <button
      v-if="canManage && service.active && service.status === 'published'"
      type="button"
      class="link inline-flex items-center gap-1"
      :disabled="pausing"
      :title="
        service.paused
          ? 'Start scheduled polls again and refresh its page.'
          : 'Stop scheduled polls and keep the last known state on its page.'
      "
      @click="emit('pause')"
    >
      <span v-if="pausing" class="loading loading-spinner loading-xs" />
      {{ service.paused ? 'Resume polling' : 'Pause polling' }}
    </button>

    <button
      v-if="canManage && service.active && service.status === 'no_match'"
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
      v-if="canManage && service.active && service.status === 'proposed'"
      type="button"
      class="link"
      @click="emit('publish')"
    >
      Publish
    </button>

    <button v-if="canManage && !service.active" type="button" class="link" @click="emit('restore')">
      Restore
    </button>

    <button
      v-if="!service.active && canPurge"
      type="button"
      class="link text-error"
      title="Delete this service and everything it has recorded. This cannot be undone."
      @click="emit('purge')"
    >
      Force delete
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
