<script setup lang="ts">
const props = withDefaults(defineProps<{ status: string; size?: 'xs' | 'sm' | 'md' }>(), {
  size: 'md',
})

const META: Record<string, { label: string; cls: string }> = {
  ok: { label: 'Operational', cls: 'badge-success' },
  maintenance: { label: 'Maintenance', cls: 'badge-info' },
  degraded: { label: 'Degraded', cls: 'badge-warning' },
  partial: { label: 'Partial outage', cls: 'badge-warning' },
  major: { label: 'Major outage', cls: 'badge-error' },
  unknown: { label: 'Unknown', cls: 'badge-ghost' },
}

const meta = computed(() => META[props.status] ?? META.unknown!)

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
  <span class="badge font-medium" :class="[meta.cls, sizeClass, size === 'xs' ? 'gap-1' : 'gap-1.5']">
    <span class="inline-block rounded-full bg-current" :class="size === 'xs' ? 'h-1 w-1' : 'h-1.5 w-1.5'" />
    {{ meta.label }}
  </span>
</template>
