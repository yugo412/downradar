<script setup lang="ts">
import type { VendorDetail } from '~/types/api'

const { canManageServices } = useAuth()

const route = useRoute()
const slug = computed(() => String(route.params.vendor ?? ''))

const { data: vendor, error, refresh } = await useFetch<VendorDetail>(
  () => `/api/v1/vendors/${slug.value}`,
)

const editing = ref(false)

async function onSaved() {
  editing.value = false
  await refresh()
}

useHead({ titleTemplate: null })

if (vendor.value) {
  const name = vendor.value.name
  useSeoMeta({
    title: `Is ${name} down right now?`,
    description: `Live ${name} status and recent incidents, taken from the official status page. Check whether ${name} is down right now.`,
    ogTitle: `Is ${name} down right now?`,
    ogDescription: `Live status of ${name}, straight from the official status page.`,
    ogType: 'website',
  })
}

</script>

<template>
  <div class="mx-auto max-w-6xl px-4 py-8">
    <NuxtLink to="/" class="link link-hover text-sm opacity-70">&larr; All services</NuxtLink>

    <div v-if="error" role="alert" class="alert alert-error mt-6">
      <span>Service not found, or the API is still warming up.</span>
    </div>

    <template v-else-if="vendor">
      <header class="mt-4 mb-8 flex flex-wrap items-start justify-between gap-4">
        <div class="flex items-start gap-4 min-w-0">
          <VendorLogo :src="vendor.logoUrl" :name="vendor.name" :size="56" />
          <div class="min-w-0">
            <h1 class="text-3xl font-bold tracking-tight">
              Is {{ vendor.name }} down right now?
            </h1>
            <p class="mt-1 text-sm opacity-70">
              Source:
              <a :href="vendor.url" target="_blank" rel="noopener noreferrer" class="link">
                {{ vendor.url }}
              </a>
            </p>
          </div>
        </div>
        <div class="flex items-center gap-2">
          <ClientOnly>
            <button
              v-if="canManageServices"
              type="button"
              class="btn btn-ghost btn-sm"
              @click="editing = true"
            >
              Quick edit
            </button>
          </ClientOnly>
          <StatusBadge :status="vendor.status" />
        </div>
      </header>

      <VendorStatus :vendor="vendor" />

      <ClientOnly>
        <ServiceEditor
          v-if="editing && canManageServices"
          :slug="vendor.slug"
          @close="editing = false"
          @saved="onSaved"
        />
      </ClientOnly>
    </template>

    <div v-else class="flex items-center gap-3 py-16 justify-center opacity-70">
      <span class="loading loading-spinner" />
      <span>Loading…</span>
    </div>
  </div>
</template>
