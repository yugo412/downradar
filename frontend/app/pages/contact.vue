<script setup lang="ts">
definePageMeta({ ssr: false })

useSeoMeta({
  title: 'Contact',
  description: 'Send a question, a correction or a service to add to the people who run DownRadar.',
})

const { user } = useAuth()

const name = ref('')
const email = ref('')
const subject = ref('')
const body = ref('')
const busy = ref(false)
const error = ref('')
const sent = ref(false)

// A signed in visitor almost always wants to write from their own address, so
// the form is filled in for them. Anything already typed is left alone.
watch(
  user,
  (u) => {
    if (!u) return
    if (!name.value) name.value = u.name
    if (!email.value) email.value = u.email
  },
  { immediate: true },
)

async function submit() {
  busy.value = true
  error.value = ''

  try {
    await $fetch('/api/v1/contact', {
      method: 'POST',
      body: {
        name: name.value.trim(),
        email: email.value.trim(),
        subject: subject.value.trim(),
        body: body.value.trim(),
      },
    })
    sent.value = true
  } catch (err) {
    error.value = apiError(err, 'Could not send the message.')
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="mx-auto max-w-2xl px-4 py-8">
    <h1 class="text-2xl font-bold tracking-tight">Contact</h1>
    <p class="mt-1 text-sm opacity-70">
      A question, a correction, or a service that should be on the site? Send it here and it lands
      in the inbox the site is run from.
    </p>

    <div class="mt-6 rounded-box border border-base-300 bg-base-100 p-5 shadow-sm">
      <div v-if="sent" class="flex flex-col gap-3">
        <p class="text-sm">
          Thanks, your message is on its way. The reply comes to the address you gave.
        </p>
        <NuxtLink to="/" class="btn btn-primary btn-sm self-start">Back to the services</NuxtLink>
      </div>

      <form v-else class="flex flex-col gap-4" @submit.prevent="submit">
        <label class="form-control w-full">
          <span class="label-text mb-1 text-sm font-medium">Your name</span>
          <input
            v-model="name"
            type="text"
            class="input input-bordered w-full"
            maxlength="100"
            autocomplete="name"
            required
            autofocus
          />
        </label>

        <label class="form-control w-full">
          <span class="label-text mb-1 text-sm font-medium">Your email</span>
          <input
            v-model="email"
            type="email"
            class="input input-bordered w-full"
            autocomplete="email"
            required
          />
          <span class="label-text-alt mt-1 opacity-70">Replies go here.</span>
        </label>

        <label class="form-control w-full">
          <span class="label-text mb-1 text-sm font-medium">Subject</span>
          <input v-model="subject" type="text" class="input input-bordered w-full" maxlength="200" required />
        </label>

        <label class="form-control w-full">
          <span class="label-text mb-1 text-sm font-medium">Message</span>
          <textarea
            v-model="body"
            class="textarea textarea-bordered w-full"
            rows="6"
            maxlength="5000"
            required
          />
        </label>

        <p v-if="error" role="alert" class="text-sm text-error">{{ error }}</p>

        <div class="flex justify-end">
          <button type="submit" class="btn btn-primary btn-sm" :disabled="busy">
            <span v-if="busy" class="loading loading-spinner loading-xs" />
            Send message
          </button>
        </div>
      </form>
    </div>
  </div>
</template>
