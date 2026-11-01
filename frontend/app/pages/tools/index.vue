<script setup lang="ts">
definePageMeta({ middleware: 'admin', ssr: false })

useSeoMeta({ title: 'Tools', robots: 'noindex, nofollow' })

const title = ref('')
const message = ref('')
const sending = ref(false)
const error = ref('')
const notice = ref('')

async function send() {
  if (sending.value) return

  sending.value = true
  error.value = ''
  notice.value = ''

  try {
    const res = await $fetch<{ recipients: number }>('/api/v1/notifications/broadcast', {
      method: 'POST',
      body: { title: title.value, body: message.value },
    })
    notice.value =
      res.recipients === 1
        ? 'Sent to 1 account.'
        : `Sent to ${res.recipients} accounts.`
    title.value = ''
    message.value = ''
  } catch (err) {
    error.value = apiError(err, 'Could not send the notification.')
  } finally {
    sending.value = false
  }
}
</script>

<template>
  <div class="mx-auto max-w-6xl px-4 py-8">
    <header class="mb-6">
      <h1 class="text-2xl font-bold tracking-tight">Tools</h1>
      <p class="mt-1 text-sm opacity-70">Site-wide notices and management tools.</p>
    </header>

    <section class="rounded-box border border-base-300 bg-base-100 p-5 shadow-sm">
      <h2 class="text-lg font-semibold">Broadcast a notification</h2>
      <p class="mt-1 text-sm opacity-70">
        Sends an in-site notification to every registered account, including
        staff. Signed-out visitors never see it.
      </p>

      <p v-if="error" role="alert" class="alert alert-error mt-4">{{ error }}</p>
      <p v-if="notice" role="status" class="alert alert-success mt-4">{{ notice }}</p>

      <form class="mt-5 flex flex-col gap-4" @submit.prevent="send">
        <label class="form-control w-full">
          <span class="label-text mb-1 text-sm font-medium">Title</span>
          <input
            v-model="title"
            type="text"
            class="input input-bordered w-full"
            maxlength="160"
            required
          />
        </label>

        <label class="form-control w-full">
          <span class="label-text mb-1 text-sm font-medium">Message</span>
          <textarea
            v-model="message"
            class="textarea textarea-bordered w-full"
            rows="6"
            maxlength="4000"
            required
          />
        </label>

        <div class="flex flex-wrap items-center justify-between gap-3">
          <p class="text-xs opacity-60">Sent to every registered account.</p>
          <button
            type="submit"
            class="btn btn-primary"
            :disabled="sending || !title.trim() || !message.trim()"
          >
            <span v-if="sending" class="loading loading-spinner loading-sm" />
            Send broadcast
          </button>
        </div>
      </form>
    </section>
  </div>
</template>
