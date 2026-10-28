<script setup lang="ts">
import type { StatusCount } from '~/utils/format'

// The catalogue's filter control for a narrow screen. The states could be a row
// of chips, but six states plus the account's own two filters do not fit there
// without wrapping into a block that is hard to scan. A single trigger opens a
// bottom sheet where the options can be rows with room for their labels and
// counts. From md up the page keeps the chips in view instead.
//
// The filters are applied as they are picked, so the count in the footer is the
// list underneath the sheet already narrowed. Closing the sheet does not commit
// anything; it just puts the page back in view.
const props = withDefaults(
  defineProps<{
    statuses: StatusCount[]
    label: string
    resultCount: number
    mineCount?: number
    subscribedCount?: number
    showMine?: boolean
    showSubscribed?: boolean
  }>(),
  {
    mineCount: 0,
    subscribedCount: 0,
    showMine: false,
    showSubscribed: false,
  },
)

const picked = defineModel<string[]>({ default: () => [] })
const mine = defineModel<boolean>('mine', { default: false })
const subscribed = defineModel<boolean>('subscribed', { default: false })

const open = ref(false)
const trigger = ref<HTMLButtonElement | null>(null)
const panel = ref<HTMLElement | null>(null)

const activeCount = computed(
  () => picked.value.length + (mine.value ? 1 : 0) + (subscribed.value ? 1 : 0),
)

type ActiveChip = {
  key: string
  label: string
  count?: number
  ink?: string
  remove: () => void
}

const activeChips = computed<ActiveChip[]>(() => {
  const chips: ActiveChip[] = []

  for (const status of picked.value) {
    const option = props.statuses.find((s) => s.status === status)
    chips.push({
      key: `status-${status}`,
      label: statusMeta(status).label,
      count: option?.count,
      ink: statusMeta(status).ink,
      remove: () => toggleStatus(status),
    })
  }

  if (mine.value) {
    chips.push({
      key: 'mine',
      label: 'My services',
      count: props.mineCount,
      remove: () => {
        mine.value = false
      },
    })
  }

  if (subscribed.value) {
    chips.push({
      key: 'subscribed',
      label: 'Subscribed',
      count: props.subscribedCount,
      remove: () => {
        subscribed.value = false
      },
    })
  }

  return chips
})

function toggleStatus(status: string) {
  picked.value = picked.value.includes(status)
    ? picked.value.filter((s) => s !== status)
    : [...picked.value, status]
}

function clearAll() {
  picked.value = []
  mine.value = false
  subscribed.value = false
}

watch(open, async (isOpen) => {
  if (isOpen) {
    await nextTick()
    panel.value?.focus()
    return
  }
  trigger.value?.focus()
})
</script>

<template>
  <div class="flex flex-wrap items-center gap-2">
    <button
      ref="trigger"
      type="button"
      class="btn btn-sm border-base-300 bg-base-100"
      aria-haspopup="dialog"
      aria-controls="catalogue-filter"
      :aria-expanded="open"
      @click="open = true"
    >
      <svg
        xmlns="http://www.w3.org/2000/svg"
        class="h-4 w-4"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        stroke-width="2"
        stroke-linecap="round"
        stroke-linejoin="round"
        aria-hidden="true"
      >
        <path d="M4 6h16M7 12h10M10 18h4" />
      </svg>
      Filter
      <span v-if="activeCount" class="badge badge-primary badge-sm">{{ activeCount }}</span>
    </button>

    <button
      v-for="chip in activeChips"
      :key="chip.key"
      type="button"
      class="inline-flex items-center gap-1.5 rounded-full border border-base-300 bg-base-100 px-2.5 py-1 text-xs hover:border-base-content/30"
      :aria-label="`Remove ${chip.label} filter`"
      @click="chip.remove"
    >
      <span
        v-if="chip.ink"
        class="inline-block h-2 w-2 shrink-0 rounded-full bg-current"
        :class="chip.ink"
      />
      <span>{{ chip.label }}</span>
      <span v-if="chip.count !== undefined" class="tabular-nums opacity-50">({{ chip.count }})</span>
      <span aria-hidden="true" class="opacity-50">×</span>
    </button>

    <button v-if="activeCount" type="button" class="link text-xs" @click="clearAll">
      Clear all
    </button>

    <Teleport to="body">
      <div
        v-if="open"
        class="fixed inset-0 z-50 flex items-end justify-center bg-black/50 md:hidden"
      role="dialog"
      aria-modal="true"
      aria-labelledby="catalogue-filter-title"
      @click.self="open = false"
      @keydown.esc.stop.prevent="open = false"
    >
      <div
        id="catalogue-filter"
        ref="panel"
        tabindex="-1"
        class="flex max-h-[85vh] w-full max-w-md flex-col overflow-hidden rounded-t-box border border-base-300 bg-base-100 shadow-xl outline-none"
      >
        <div class="mx-auto mt-2 h-1 w-10 rounded-full bg-base-300" />

        <div class="flex items-center justify-between border-b border-base-300 px-5 py-3">
          <h2 id="catalogue-filter-title" class="text-lg font-semibold">{{ props.label }}</h2>
          <button type="button" class="btn btn-ghost btn-sm" aria-label="Close" @click="open = false">
            ✕
          </button>
        </div>

        <div class="min-h-0 flex-1 overflow-y-auto px-5 py-4">
          <section>
            <div class="mb-2 flex items-baseline justify-between gap-3">
              <h3 class="text-sm font-semibold">Status</h3>
              <span class="text-xs opacity-50">Pick one or more</span>
            </div>

            <div class="space-y-1">
              <label
                v-for="option in props.statuses"
                :key="option.status"
                class="flex cursor-pointer items-center gap-3 rounded-field px-2 py-2 text-sm hover:bg-base-200"
              >
                <input
                  type="checkbox"
                  class="checkbox checkbox-sm"
                  :checked="picked.includes(option.status)"
                  @change="toggleStatus(option.status)"
                >
                <span
                  class="inline-block h-2 w-2 shrink-0 rounded-full bg-current"
                  :class="statusMeta(option.status).ink"
                />
                <span class="flex-1">{{ statusMeta(option.status).label }}</span>
                <span class="tabular-nums opacity-50">{{ option.count }}</span>
              </label>
            </div>
          </section>

          <section
            v-if="props.showMine || props.showSubscribed"
            class="mt-5 border-t border-base-300 pt-4"
          >
            <h3 class="mb-2 text-sm font-semibold">Your list</h3>

            <div class="space-y-1">
              <label
                v-if="props.showMine"
                class="flex cursor-pointer items-center gap-3 rounded-field px-2 py-2 text-sm hover:bg-base-200"
              >
                <input v-model="mine" type="checkbox" class="checkbox checkbox-sm">
                <span class="flex-1">My services</span>
                <span class="tabular-nums opacity-50">{{ props.mineCount }}</span>
              </label>

              <label
                v-if="props.showSubscribed"
                class="flex cursor-pointer items-center gap-3 rounded-field px-2 py-2 text-sm hover:bg-base-200"
              >
                <input v-model="subscribed" type="checkbox" class="checkbox checkbox-sm">
                <span class="flex-1">Subscribed</span>
                <span class="tabular-nums opacity-50">{{ props.subscribedCount }}</span>
              </label>
            </div>
          </section>
        </div>

        <div class="flex items-center justify-between gap-3 border-t border-base-300 px-5 py-3">
          <button
            type="button"
            class="btn btn-ghost btn-sm"
            :disabled="!activeCount"
            @click="clearAll"
          >
            Reset
          </button>
          <button type="button" class="btn btn-primary btn-sm" @click="open = false">
            Show {{ props.resultCount }} {{ props.resultCount === 1 ? 'service' : 'services' }}
          </button>
        </div>
      </div>
      </div>
    </Teleport>
  </div>
</template>
