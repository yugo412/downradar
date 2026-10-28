<script setup lang="ts">
const props = defineProps<{ at?: string | null }>()

const { format } = useLocalTime()
const label = computed(() => format(props.at))
</script>

<template>
  <!-- UTC on the server and through hydration. The zone switches to the
       visitor's own once this has mounted, without changing what a cached page
       carries. -->
  <time :datetime="props.at ?? undefined">{{ label }}</time>
</template>
