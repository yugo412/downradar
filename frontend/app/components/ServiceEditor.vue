<script setup lang="ts">
import type { AdminProfile, AdminService, ServicePayload, ServiceStatus } from '~/types/api'

const props = withDefaults(
  defineProps<{
    slug: string
    presetStatus?: ServiceStatus
    heading?: string
  }>(),
  { presetStatus: undefined, heading: '' },
)

const emit = defineEmits<{
  close: []
  saved: [service: AdminService]
}>()

const { canManageServices } = useAuth()

const profiles = ref<AdminProfile[]>([])
const categories = ref<string[]>([])
const service = ref<AdminService | null>(null)
const loading = ref(true)
const busy = ref(false)
const error = ref('')

onMounted(async () => {
  try {
    const [profileResult, categoryResult, serviceResult] = await Promise.all([
      $fetch<{ profiles: AdminProfile[] }>('/api/v1/profiles'),
      $fetch<{ categories: string[] }>('/api/v1/categories'),
      $fetch<{ service: AdminService }>(`/api/v1/services/${props.slug}`),
    ])
    profiles.value = profileResult.profiles
    categories.value = categoryResult.categories
    service.value = props.presetStatus
      ? { ...serviceResult.service, status: props.presetStatus }
      : serviceResult.service
  } catch (err) {
    error.value = apiError(err, 'Could not load the service.')
  } finally {
    loading.value = false
  }
})

async function save(payload: ServicePayload) {
  busy.value = true
  error.value = ''

  try {
    const res = await $fetch<{ service: AdminService }>(
      `/api/v1/services/${props.slug}`,
      { method: 'PATCH', body: payload },
    )
    emit('saved', res.service)
  } catch (err) {
    error.value = apiError(err)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div
    class="fixed inset-0 z-50 overflow-y-auto bg-black/50 p-4"
    role="dialog"
    aria-modal="true"
    @click.self="emit('close')"
  >
    <div class="mx-auto my-8 w-full max-w-2xl rounded-box border border-base-300 bg-base-100 shadow-xl">
      <div class="flex items-center justify-between border-b border-base-300 px-5 py-3">
        <h2 class="text-lg font-semibold">
          {{ heading || (service ? `Edit ${service.name}` : 'Edit service') }}
        </h2>
        <button type="button" class="btn btn-ghost btn-sm" aria-label="Close" @click="emit('close')">
          ✕
        </button>
      </div>

      <div class="p-5">
        <div v-if="loading" class="flex justify-center py-10">
          <span class="loading loading-spinner" />
        </div>
        <ServiceForm
          v-else
          :key="service?.slug ?? 'missing'"
          :initial="service"
          :profiles="profiles"
          :categories="categories"
          :can-choose-visibility="canManageServices"
          submit-label="Save changes"
          :busy="busy"
          :error="error"
          show-status
          @submit="save"
          @cancel="emit('close')"
        />
      </div>
    </div>
  </div>
</template>
