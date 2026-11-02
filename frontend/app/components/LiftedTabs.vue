<script setup lang="ts">
defineProps<{ label: string }>()

const root = ref<HTMLElement | null>(null)
let shown: HTMLElement | null = null

// The strip scrolls sideways when the labels do not fit, so a tab near the right
// edge is partly hidden until it is the one selected. Move the selected tab
// toward the middle of the strip when it fits, and stop at either end when it
// does not, so the tabs on both sides of it stay in view. This only runs when the
// selection changes, so scrolling the strip by hand is not undone by the next
// update.
function revealActive() {
  const active =
    root.value?.querySelector<HTMLElement>('.tabs > label:has(:checked)') ??
    root.value?.querySelector<HTMLElement>('.tabs > .tab-active')
  if (!active || active === shown) return
  shown = active

  const bar = active.closest<HTMLElement>('.lifted-tabs-bar')
  if (!bar) return

  const barBox = bar.getBoundingClientRect()
  const tabBox = active.getBoundingClientRect()
  const middle = bar.scrollLeft + (tabBox.left - barBox.left) - (bar.clientWidth - tabBox.width) / 2
  const furthest = bar.scrollWidth - bar.clientWidth
  bar.scrollLeft = Math.min(Math.max(middle, 0), furthest)
}

onMounted(revealActive)
onUpdated(revealActive)
</script>

<template>
  <div ref="root" class="lifted-tabs">
    <div class="lifted-tabs-bar overflow-x-auto">
      <div class="tabs tabs-lift w-max min-w-full flex-nowrap" role="tablist" :aria-label="label">
        <slot name="tabs" />
      </div>
    </div>
    <div class="lifted-tabs-panel rounded-box border border-base-300 bg-base-100 p-6">
      <slot />
    </div>
  </div>
</template>
