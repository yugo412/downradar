<script setup lang="ts">
const props = withDefaults(defineProps<{ status: string; size?: 'sm' | 'md' }>(), {
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
</script>

<template>
  <span
    class="badge gap-1.5 font-medium"
    :class="[meta.cls, size === 'sm' ? 'badge-sm' : 'badge-md']"
  >
    <span class="inline-block h-1.5 w-1.5 rounded-full bg-current" />
    {{ meta.label }}
  </span>
</template>
