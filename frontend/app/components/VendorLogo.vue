<script setup lang="ts">
const props = withDefaults(
  defineProps<{ src?: string | null; name: string; size?: number }>(),
  { size: 40 },
)

const broken = ref(false)
const usable = computed(() => Boolean(props.src) && !broken.value)
</script>

<template>
  <!-- The reset makes an image's height auto, so pin both sides in CSS; an SVG's
       own aspect ratio must not decide the size of the outer box. -->
  <img
    v-if="usable"
    :src="src!"
    :alt="`${name} logo`"
    :width="size"
    :height="size"
    :style="{ width: `${size}px`, height: `${size}px` }"
    class="shrink-0 rounded-box border border-base-300 bg-base-100 object-contain p-1"
    loading="lazy"
    referrerpolicy="no-referrer"
    @error="broken = true"
  />
  <span
    v-else
    class="grid shrink-0 place-items-center rounded-box border border-base-300 bg-base-100 text-base-content/40"
    :style="{ width: `${size}px`, height: `${size}px` }"
    :title="`No logo available for ${name}`"
  >
    <svg
      :width="size * 0.55"
      :height="size * 0.55"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      stroke-width="1.6"
      stroke-linecap="round"
      stroke-linejoin="round"
      role="img"
      :aria-label="`${name}, no logo`"
    >
      <circle cx="12" cy="12" r="9" />
      <path d="M3 12h18" />
      <path d="M12 3c2.5 2.6 3.9 5.7 3.9 9s-1.4 6.4-3.9 9c-2.5-2.6-3.9-5.7-3.9-9S9.5 5.6 12 3z" />
    </svg>
  </span>
</template>
