<script setup lang="ts">
import type { AdminService, VendorDetail } from '~/types/api'

definePageMeta({ middleware: 'authenticated', ssr: false })

const route = useRoute()
const slug = computed(() => String(route.params.slug ?? ''))

const {
  data: detail,
  error,
  refresh,
} = useFetch<VendorDetail>(() => `/api/v1/services/${slug.value}/status`)

const { data: meta, refresh: refreshMeta } = useFetch<{ service: AdminService }>(
  () => `/api/v1/services/${slug.value}`,
)

const editing = ref(false)

async function onSaved() {
  editing.value = false
  await Promise.all([refresh(), refreshMeta()])
}
</script>

<template>
  <div class="mx-auto max-w-6xl px-4 py-8">
    <NuxtLink to="/services" class="link link-hover text-sm opacity-70">
      &larr; All services
    </NuxtLink>

    <div v-if="error" role="alert" class="alert alert-error mt-6">
      <span>This service could not be loaded. It may belong to somebody else.</span>
    </div>

    <template v-else-if="detail">
      <header class="mt-4 mb-8 flex flex-wrap items-start justify-between gap-4">
        <div class="flex items-start gap-4 min-w-0">
          <VendorLogo :src="detail.logoUrl" :name="detail.name" :size="56" />
          <div class="min-w-0">
            <h1 class="text-3xl font-bold tracking-tight">{{ detail.name }}</h1>
            <div class="mt-1 flex flex-wrap items-center gap-2 text-sm opacity-70">
              <span v-if="meta?.service.ownerId" class="badge badge-ghost badge-sm">
                private &middot; {{ meta.service.ownerName }}
              </span>
              <a
                v-if="detail.url"
                :href="detail.url"
                target="_blank"
                rel="noopener noreferrer"
                class="link break-all"
              >
                {{ detail.url }}
              </a>
            </div>
          </div>
        </div>
        <div class="flex items-center gap-2">
          <button type="button" class="btn btn-ghost btn-sm" @click="editing = true">
            Quick edit
          </button>
          <StatusBadge :status="detail.status" />
        </div>
      </header>

      <VendorStatus :vendor="detail" />

      <ServiceEditor v-if="editing" :slug="slug" @close="editing = false" @saved="onSaved" />
    </template>

    <div v-else class="flex items-center justify-center gap-3 py-16 opacity-70">
      <span class="loading loading-spinner" />
      <span>Loading&hellip;</span>
    </div>
  </div>
</template>
