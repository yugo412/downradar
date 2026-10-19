<script setup lang="ts">
import type { AccountAlert } from '~/types/api'

defineProps<{
  alert: AccountAlert
  busy?: boolean
}>()

const emit = defineEmits<{
  state: [state: AccountAlert['state']]
  remove: []
}>()

const open = ref(false)
const trigger = ref<HTMLElement | null>(null)

// Where the menu is drawn, against the button it belongs to. It is placed in the
// window rather than in the row, because the table around it scrolls sideways
// inside its own box and a menu inside that box would be cut off on the rows near
// the bottom of the list.
const at = ref({ top: 0, left: 0 })
const MENU_WIDTH = 176

function place() {
  const button = trigger.value
  if (!button) return

  const rect = button.getBoundingClientRect()
  const widest = window.innerWidth - MENU_WIDTH - 8
  at.value = {
    top: Math.round(rect.bottom + 4),
    left: Math.round(Math.max(8, Math.min(rect.right - MENU_WIDTH, widest))),
  }
}

function toggle() {
  open.value = !open.value
  if (open.value) place()
}

function close() {
  open.value = false
}

// A menu that floats over the page follows it: a scroll or a resize puts it back
// under its button rather than leaving it behind.
function onKey(event: KeyboardEvent) {
  if (event.key === 'Escape') close()
}

watch(open, (isOpen) => {
  window.removeEventListener('scroll', place, true)
  window.removeEventListener('resize', place)
  window.removeEventListener('keydown', onKey)
  if (!isOpen) return

  window.addEventListener('scroll', place, true)
  window.addEventListener('resize', place)
  window.addEventListener('keydown', onKey)
})

onBeforeUnmount(() => {
  window.removeEventListener('scroll', place, true)
  window.removeEventListener('resize', place)
  window.removeEventListener('keydown', onKey)
})

function act(state: AccountAlert['state']) {
  close()
  emit('state', state)
}

function ask() {
  close()
  emit('remove')
}
</script>

<template>
  <div class="flex items-center justify-end gap-3 text-xs">
    <!-- Editing is the thing a row is most often opened for, so it stays out
         where it can be found; the rest of what a row can do waits in the menu. -->
    <NuxtLink :to="`/alerts/${alert.vendorSlug}`" class="link">
      Edit
    </NuxtLink>

    <div class="relative">
      <button
        ref="trigger"
        type="button"
        class="btn btn-ghost btn-xs px-1.5"
        aria-haspopup="true"
        :aria-expanded="open"
        :aria-label="`More actions for ${alert.vendorName}`"
        @click="toggle"
      >
        <svg class="h-4 w-4" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
          <circle cx="12" cy="5" r="1.7" />
          <circle cx="12" cy="12" r="1.7" />
          <circle cx="12" cy="19" r="1.7" />
        </svg>
      </button>

      <div v-if="open" class="fixed inset-0 z-40" @click="close" />

      <ul
        v-if="open"
        class="menu fixed z-50 w-44 rounded-box border border-base-300 bg-base-100 p-2 text-xs shadow-lg"
        :style="{ top: `${at.top}px`, left: `${at.left}px` }"
      >
        <li>
          <button
            type="button"
            :disabled="busy"
            @click="act(alert.state === 'active' ? 'paused' : 'active')"
          >
            {{ alert.state === 'active' ? 'Pause' : 'Subscribe' }}
          </button>
        </li>
        <li v-if="alert.state === 'active'">
          <button type="button" :disabled="busy" @click="act('unsubscribed')">
            Unsubscribe
          </button>
        </li>
        <li>
          <button type="button" class="text-error" :disabled="busy" @click="ask">
            Delete
          </button>
        </li>
      </ul>
    </div>
  </div>
</template>
