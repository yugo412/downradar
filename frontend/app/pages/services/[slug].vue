<script setup lang="ts">
import type { AdminService, VendorDetail } from '~/types/api'

definePageMeta({ middleware: 'staff', ssr: false })

const route = useRoute()
const slug = computed(() => String(route.params.slug ?? ''))

const { data: meta, error: metaError, refresh: refreshMeta } = useFetch<{ service: AdminService }>(
  () => `/api/v1/services/${slug.value}`,
)

// The row is the page. What the vendor reports is only there once it is polled,
// so this call is allowed to answer 404: a suggestion, or a vendor that no
// profile could read, has nothing to show but its row and its fetch log.
const { data: detail, refresh: refreshDetail } = useFetch<VendorDetail>(
  () => `/api/v1/services/${slug.value}/status`,
)

const editing = ref(false)

async function onSaved() {
  editing.value = false
  await Promise.all([refreshMeta(), refreshDetail()])
}
</script>

<template>
  <div class="mx-auto max-w-6xl px-4 py-8">
    <NuxtLink to="/services" class="link link-hover text-sm opacity-70">
      &larr; All services
    </NuxtLink>

    <div v-if="metaError" role="alert" class="alert alert-error mt-6">
      <span>This service could not be loaded. It may belong to somebody else.</span>
    </div>

    <template v-else-if="meta">
      <header class="mt-4 mb-8 flex flex-wrap items-start justify-between gap-4">
        <div class="flex items-start gap-4 min-w-0">
          <VendorLogo :src="meta.service.logoUrl" :name="meta.service.name" :size="56" />
          <div class="min-w-0">
            <h1 class="text-3xl font-bold tracking-tight">{{ meta.service.name }}</h1>
            <div class="mt-1 flex flex-wrap items-center gap-2 text-sm opacity-70">
              <span v-if="meta.service.ownerId" class="badge badge-ghost badge-sm">
                {{ meta.service.status === 'proposed' ? `suggested by ${meta.service.ownerName}` : `private · ${meta.service.ownerName}` }}
              </span>
              <a
                v-if="meta.service.url"
                :href="meta.service.url"
                target="_blank"
                rel="noopener noreferrer"
                class="link break-all"
              >
                {{ meta.service.url }}
              </a>
            </div>
          </div>
        </div>
        <div class="flex items-center gap-2">
          <button type="button" class="btn btn-ghost btn-sm gap-2" @click="editing = true">
            <svg
              xmlns="http://www.w3.org/2000/svg"
              class="h-3.5 w-3.5"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              stroke-width="2"
              stroke-linecap="round"
              stroke-linejoin="round"
              aria-hidden="true"
            >
              <path d="m16.862 4.487 1.687-1.688a1.875 1.875 0 1 1 2.652 2.652L6.832 19.82a4.5 4.5 0 0 1-1.897 1.13l-2.685.8.8-2.685a4.5 4.5 0 0 1 1.13-1.897L16.863 4.487Zm0 0L19.5 7.125" />
            </svg>
            Quick edit
          </button>
          <StatusBadge
            v-if="detail"
            :status="detail.status"
            class="order-first md:order-none"
          />
          <span
            v-else
            class="badge badge-sm order-first whitespace-nowrap md:order-none"
            :class="serviceStatusClass(meta.service.status, meta.service.active)"
          >
            {{ serviceStatusLabel(meta.service.status, meta.service.active) }}
          </span>
        </div>
      </header>

      <VendorStatus v-if="detail" :vendor="detail" />

      <p
        v-else
        class="mb-8 rounded-box border border-base-300 bg-base-100 p-6 text-center opacity-70 shadow-sm"
      >
        Nothing has been read from this vendor yet.
      </p>

      <FetchLog :slug="slug" :owner-id="meta.service.ownerId" />

      <ServiceEditor v-if="editing" :slug="slug" @close="editing = false" @saved="onSaved" />
    </template>

    <div v-else class="flex items-center justify-center gap-3 py-16 opacity-70">
      <span class="loading loading-spinner" />
      <span>Loading&hellip;</span>
    </div>
  </div>
</template>
