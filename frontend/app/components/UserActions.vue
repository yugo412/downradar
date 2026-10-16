<script setup lang="ts">
import type { AdminUser } from '~/types/api'

defineProps<{
  user: AdminUser
  me: boolean
}>()

const emit = defineEmits<{
  edit: []
  toggle: []
  remove: []
}>()
</script>

<template>
  <div class="flex flex-wrap items-center gap-2 text-xs">
    <button type="button" class="link" @click="emit('edit')">Edit</button>
    <template v-if="!me">
      <button type="button" class="link" @click="emit('toggle')">
        {{ user.disabledAt ? 'Enable' : 'Disable' }}
      </button>
      <button type="button" class="link text-error" @click="emit('remove')">Delete</button>
    </template>
  </div>
</template>
