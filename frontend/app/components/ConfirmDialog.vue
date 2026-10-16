<script setup lang="ts">
const props = withDefaults(
  defineProps<{
    heading: string
    message: string
    confirmLabel?: string
    busy?: boolean
    error?: string
  }>(),
  { confirmLabel: 'Confirm', busy: false, error: '' },
)

const emit = defineEmits<{
  confirm: []
  cancel: []
}>()
</script>

<template>
  <div
    class="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4"
    role="dialog"
    aria-modal="true"
    @click.self="emit('cancel')"
  >
    <div class="w-full max-w-md rounded-box border border-base-300 bg-base-100 shadow-xl">
      <div class="border-b border-base-300 px-5 py-3">
        <h2 class="text-lg font-semibold">{{ props.heading }}</h2>
      </div>
      <div class="px-5 py-4">
        <p class="text-sm opacity-80">{{ props.message }}</p>
        <p v-if="error" role="alert" class="mt-3 text-sm text-error">{{ error }}</p>
      </div>
      <div class="flex items-center justify-end gap-2 border-t border-base-300 px-5 py-3">
        <button type="button" class="btn btn-ghost btn-sm" :disabled="busy" @click="emit('cancel')">
          Cancel
        </button>
        <button type="button" class="btn btn-error btn-sm" :disabled="busy" @click="emit('confirm')">
          <span v-if="busy" class="loading loading-spinner loading-xs" />
          {{ confirmLabel }}
        </button>
      </div>
    </div>
  </div>
</template>
