<script setup lang="ts">
const props = defineProps<{ at?: string | null; long?: boolean }>()

const now = useNow()
const ready = ref(false)

onMounted(() => {
  ready.value = true
})

const label = computed(() =>
  props.long ? timeAgoLong(props.at, now.value) : timeAgo(props.at, now.value),
)
</script>

<template>
  <!-- Nothing until the browser has it. The page is cached, so a value rendered
       on the server would be handed to the next visitor and would never move. -->
  <span v-if="ready">{{ label }}</span>
</template>
