<script setup lang="ts">
const props = withDefaults(defineProps<{ status: string; size?: 'xs' | 'sm' | 'md' }>(), {
  size: 'md',
})

const meta = computed(() => statusMeta(props.status))

const sizeClass = computed(() => {
  switch (props.size) {
    case 'xs':
      return 'badge-xs'
    case 'sm':
      return 'badge-sm'
    default:
      return 'badge-md'
  }
})
</script>

<template>
  <span
    class="badge whitespace-nowrap font-medium"
    :class="[meta.cls, sizeClass, size === 'xs' ? 'gap-1' : 'gap-1.5']"
  >
    <span class="inline-block rounded-full bg-current" :class="size === 'xs' ? 'h-1 w-1' : 'h-1.5 w-1.5'" />
    {{ meta.label }}
  </span>
</template>
