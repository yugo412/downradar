<script setup lang="ts">
// The feed is the way of following a vendor that needs nothing from the site:
// there is no account behind it, nothing is subscribed to and nothing is stored,
// so there is nothing to switch off and nothing to end. What this offers is the
// address of the feed and a button that copies it.
const props = defineProps<{ slug: string; name: string }>()

const config = useRuntimeConfig()
const siteUrl = computed(() => String(config.public.siteUrl).replace(/\/$/, ''))

// The same changes in the two shapes a reader can ask for. XML is RSS, which is
// what the name usually means, and JSON is JSON Feed for a reader that would
// rather have that.
const formats: ('xml' | 'json')[] = ['xml', 'json']
const format = ref<'xml' | 'json'>('xml')
const feedUrl = computed(() => `${siteUrl.value}/${props.slug}/rss.${format.value}`)

// The address is meant to be pasted into a reader somewhere else, so the button
// hands it over as it is shown and says so for a moment.
const copied = ref(false)
const error = ref('')
let copiedTimer: ReturnType<typeof setTimeout> | undefined

async function copyUrl() {
  error.value = ''

  try {
    await navigator.clipboard.writeText(feedUrl.value)
    copied.value = true
    clearTimeout(copiedTimer)
    copiedTimer = setTimeout(() => {
      copied.value = false
    }, 2000)
  } catch {
    error.value = 'The address could not be copied. Select it and copy it by hand.'
  }
}

onBeforeUnmount(() => clearTimeout(copiedTimer))
</script>

<template>
  <div class="w-full">
    <p class="text-sm opacity-70">
      A feed of every status change for {{ name }}, for a reader of your own. It
      needs no account and nothing is stored here, so there is nothing to switch
      off: the feed stops when you take it out of your reader.
    </p>

    <div role="tablist" class="tabs tabs-box mt-3 w-fit">
      <button
        v-for="option in formats"
        :key="option"
        type="button"
        role="tab"
        class="tab"
        :class="{ 'tab-active': format === option }"
        @click="format = option"
      >
        {{ option.toUpperCase() }}
      </button>
    </div>

    <div class="join mt-3 w-full">
      <input
        :value="feedUrl"
        readonly
        aria-label="Feed URL"
        class="input input-bordered join-item w-full min-w-0 flex-1"
      >
      <button
        type="button"
        class="btn btn-outline join-item shrink-0 !border-base-content/20"
        @click="copyUrl"
      >
        {{ copied ? 'Copied' : 'Copy URL' }}
      </button>
    </div>

    <p class="mt-2 text-sm opacity-70">
      XML is RSS and JSON is JSON Feed. Both carry the same changes.
    </p>
    <p v-if="error" role="alert" class="mt-2 text-sm text-error">{{ error }}</p>
  </div>
</template>
