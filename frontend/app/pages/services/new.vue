<script setup lang="ts">
import type { AdminProfile, ServiceCreated, ServicePayload } from '~/types/api'

definePageMeta({ middleware: 'staff', ssr: false })

useSeoMeta({ title: 'Add service', robots: 'noindex, nofollow' })

const { canManageServices } = useAuth()

const profiles = ref<AdminProfile[]>([])
const categories = ref<string[]>([])
const loading = ref(true)
const busy = ref(false)
const error = ref('')

onMounted(async () => {
  try {
    const [profileRes, categoryRes] = await Promise.all([
      $fetch<{ profiles: AdminProfile[] }>('/api/v1/profiles'),
      $fetch<{ categories: string[] }>('/api/v1/categories'),
    ])
    profiles.value = profileRes.profiles
    categories.value = categoryRes.categories
  } catch (err) {
    error.value = apiError(err, 'Could not load the form options.')
  } finally {
    loading.value = false
  }
})

async function save(payload: ServicePayload) {
  busy.value = true
  error.value = ''

  try {
    const res = await $fetch<ServiceCreated>('/api/v1/services', {
      method: 'POST',
      body: payload,
    })
    await navigateTo(`/services?created=${res.slug}`)
  } catch (err) {
    error.value = apiError(err)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="mx-auto max-w-2xl px-4 py-8">
    <NuxtLink to="/services" class="link link-hover text-sm opacity-70">
      &larr; All services
    </NuxtLink>

    <h1 class="mt-4 text-2xl font-bold tracking-tight">Add service</h1>
    <p class="mt-1 text-sm opacity-70">
      The service is published straight away and picked up by the poller on its next pass.
    </p>

    <div class="mt-6 rounded-box border border-base-300 bg-base-100 p-5 shadow-sm">
      <div v-if="loading" class="flex justify-center py-10">
        <span class="loading loading-spinner" />
      </div>
      <ServiceForm
        v-else
        :profiles="profiles"
        :categories="categories"
        :can-choose-visibility="canManageServices"
        submit-label="Add service"
        :busy="busy"
        :error="error"
        @submit="save"
        @cancel="navigateTo('/services')"
      />
    </div>
  </div>
</template>
