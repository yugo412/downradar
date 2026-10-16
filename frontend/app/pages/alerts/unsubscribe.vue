<script setup lang="ts">
import type { AlertSubscription } from '~/types/api'

// The link in the email footer opens this page instead of stopping the alerts by
// itself: mail clients and scanners fetch the links they are sent, and a link
// that acted on a fetch would unsubscribe people who never asked.
//
// It is rendered in the browser only, so the answer for one token is never
// cached and handed to another visitor.
definePageMeta({ ssr: false })

useHead({
  title: 'Email alerts',
  meta: [{ name: 'robots', content: 'noindex' }],
})

const route = useRoute()
const token = computed(() => String(route.query.token ?? ''))

const { data, error } = await useFetch<AlertSubscription>(
  () => `/api/v1/alerts/${token.value}`,
  { immediate: !!token.value },
)

const busy = ref(false)
const problem = ref('')
const stopped = ref(false)

async function unsubscribe() {
  busy.value = true
  problem.value = ''

  try {
    await $fetch(`/api/v1/alerts/${token.value}`, { method: 'DELETE' })
    stopped.value = true
  } catch (err) {
    problem.value = apiError(err)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="mx-auto max-w-xl px-4 py-16">
    <NuxtLink to="/" class="link link-hover text-sm opacity-70">&larr; All services</NuxtLink>

    <div class="mt-4 rounded-box border border-base-300 bg-base-100 p-6 shadow-sm">
      <h1 class="text-xl font-semibold">Email alerts</h1>

      <p v-if="!token" class="mt-3 text-sm opacity-80">
        This link has no token in it. Open the unsubscribe link at the bottom of one of the
        emails again.
      </p>

      <p v-else-if="error" class="mt-3 text-sm opacity-80">
        This link is not valid any more. If you still get the emails, use the unsubscribe link in
        the last one you received.
      </p>

      <template v-else-if="data">
        <p v-if="stopped || data.unsubscribedAt" class="mt-3 text-sm">
          You will not get any more emails about {{ data.vendorName }}.
        </p>

        <template v-else>
          <p class="mt-3 text-sm opacity-80">
            Stop the emails about <strong>{{ data.vendorName }}</strong>? They are sent to
            {{ data.email }}.
          </p>
          <p v-if="problem" role="alert" class="mt-3 text-sm text-error">{{ problem }}</p>

          <div class="mt-5 flex flex-wrap items-center gap-2">
            <button type="button" class="btn btn-error btn-sm" :disabled="busy" @click="unsubscribe">
              <span v-if="busy" class="loading loading-spinner loading-xs" />
              Unsubscribe
            </button>
            <NuxtLink :to="`/${data.vendorSlug}`" class="btn btn-ghost btn-sm">
              Keep the alerts
            </NuxtLink>
          </div>
        </template>
      </template>

      <div v-else class="mt-3 flex items-center gap-3 text-sm opacity-70">
        <span class="loading loading-spinner loading-xs" />
        Loading&hellip;
      </div>
    </div>
  </div>
</template>
