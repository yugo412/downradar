<script setup lang="ts">
// The one moment a signing secret can be read: it is made by pressing Generate,
// shown until this closes, and gone from the page afterwards. The site keeps what
// it needs to sign with and never hands it out again, so a secret that is lost is
// replaced rather than found.
const props = defineProps<{
  secret: string
  busy: boolean
  error: string
  rotating: boolean
}>()

const emit = defineEmits<{
  generate: []
  close: []
}>()

const copied = ref(false)
let timer: ReturnType<typeof setTimeout> | undefined

async function copy() {
  try {
    await navigator.clipboard.writeText(props.secret)
    copied.value = true
    clearTimeout(timer)
    timer = setTimeout(() => {
      copied.value = false
    }, 2000)
  } catch {
    copied.value = false
  }
}

onBeforeUnmount(() => clearTimeout(timer))
</script>

<template>
  <div
    class="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4"
    role="dialog"
    aria-modal="true"
    aria-labelledby="webhook-secret-heading"
    @click.self="emit('close')"
  >
    <div class="w-full max-w-lg rounded-box border border-base-300 bg-base-100 shadow-xl">
      <div class="border-b border-base-300 px-5 py-3">
        <h2 id="webhook-secret-heading" class="text-lg font-semibold">Signing secret</h2>
      </div>

      <div class="px-5 py-4">
        <template v-if="!props.secret">
          <p class="text-sm opacity-80">
            Every call is signed with a secret that only you and this site know, so your end can
            tell a real call from anything else that finds its way there.
          </p>
          <p class="mt-3 text-sm opacity-80">
            Generating saves the alert as it stands and shows the secret once.
            <template v-if="props.rotating">
              The secret that is set now keeps working for a day, which gives your end time to
              change over.
            </template>
          </p>
          <p v-if="props.error" role="alert" class="mt-3 text-sm text-error">{{ props.error }}</p>
        </template>

        <template v-else>
          <p class="text-sm opacity-80">
            Copy this now. It is shown once, and this is the only time it can be read.
          </p>
          <div class="join mt-3 w-full">
            <code
              class="join-item flex min-w-0 flex-1 items-center border border-base-300 bg-base-200 px-3 py-2 text-xs break-all"
              >{{ props.secret }}</code
            >
            <button type="button" class="btn btn-outline btn-sm join-item" @click="copy">
              {{ copied ? 'Copied' : 'Copy' }}
            </button>
          </div>
          <div role="status" class="mt-3 rounded-box border border-warning/40 bg-warning/10 p-3 text-sm">
            Keep it somewhere safe, like a password manager or the settings of the service that
            receives the calls. If you lose it, generate a new one: the old one stops working after
            a day, and this one will not be shown again.
          </div>
        </template>
      </div>

      <div class="flex items-center justify-end gap-2 border-t border-base-300 px-5 py-3">
        <template v-if="!props.secret">
          <button type="button" class="btn btn-ghost btn-sm" :disabled="props.busy" @click="emit('close')">
            Cancel
          </button>
          <button type="button" class="btn btn-primary btn-sm" :disabled="props.busy" @click="emit('generate')">
            <span v-if="props.busy" class="loading loading-spinner loading-xs" />
            Generate
          </button>
        </template>
        <button v-else type="button" class="btn btn-primary btn-sm" @click="emit('close')">Done</button>
      </div>
    </div>
  </div>
</template>
