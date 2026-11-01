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
  changed: [service: AdminService]
}>()

const { canManageServices } = useAuth()

const profiles = ref<AdminProfile[]>([])
const categories = ref<string[]>([])
const service = ref<AdminService | null>(null)
const loading = ref(true)
const busy = ref(false)
const error = ref('')

// Pausing is the same change the service page makes, kept beside the form so a
// pause does not have to be saved with the fields. It is offered only where the
// API accepts it, so the sheet never shows a control that would be refused.
const pausing = ref(false)
const pauseError = ref('')
const canPause = computed(
  () =>
    canManageServices.value &&
    Boolean(service.value?.active) &&
    service.value?.status === 'published',
)

async function togglePause() {
  if (!service.value) return

  pausing.value = true
  pauseError.value = ''
  try {
    const res = await $fetch<{ service: AdminService }>(
      `/api/v1/services/${props.slug}`,
      { method: 'PATCH', body: { paused: !service.value.paused } },
    )
    service.value = res.service
    emit('changed', res.service)
  } catch (err) {
    pauseError.value = apiError(err, 'Could not change polling.')
  } finally {
    pausing.value = false
  }
}

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
        <template v-else>
          <div
            v-if="canPause && service"
            class="mb-5 flex flex-wrap items-start justify-between gap-3 rounded-box border border-base-300 p-4"
          >
            <div>
              <p class="text-sm font-medium">Scheduled polling</p>
              <p class="text-sm opacity-70">
                {{
                  service.paused
                    ? 'Paused. The last known state stays on the page.'
                    : 'Running on its usual schedule.'
                }}
              </p>
            </div>
            <button
              type="button"
              class="btn btn-sm gap-2"
              :disabled="pausing || busy"
              :title="
                service.paused
                  ? 'Start scheduled polls again and refresh its page.'
                  : 'Stop scheduled polls and keep the last known state on its page.'
              "
              @click="togglePause"
            >
              <span v-if="pausing" class="loading loading-spinner loading-xs" />
              {{ service.paused ? 'Resume polling' : 'Pause polling' }}
            </button>
            <p v-if="pauseError" role="alert" class="w-full text-sm text-error">
              {{ pauseError }}
            </p>
          </div>

          <ServiceForm
            :key="service?.slug ?? 'missing'"
            :initial="service"
            :profiles="profiles"
            :categories="categories"
            show-visibility
            :can-choose-profile="canManageServices"
            :show-slug="canManageServices"
            submit-label="Save changes"
            :busy="busy || pausing"
            :error="error"
            :show-status="canManageServices"
            @submit="save"
            @cancel="emit('close')"
          />
        </template>
      </div>
    </div>
  </div>
</template>
