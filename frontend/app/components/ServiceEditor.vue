<script setup lang="ts">
import type { AdminProfile, AdminService, ServicePayload, ServiceStatus } from '~/types/api'

const props = withDefaults(
  defineProps<{
    slug: string
    presetStatus?: ServiceStatus
    heading?: string
    // The vendor page's quick edit is the only sheet that offers removal; the
    // services list and detail screens already have their own row actions.
    showRemove?: boolean
  }>(),
  { presetStatus: undefined, heading: '', showRemove: false },
)

const emit = defineEmits<{
  close: []
  saved: [service: AdminService]
  changed: [service: AdminService]
  removed: []
}>()

const { canManageServices, isAdmin } = useAuth()

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
    (service.value?.status === 'published' || service.value?.status === 'no_match'),
)

// Removing is an admin's alone, and the button only appears where the sheet was
// asked to carry it. The confirmation says what happens to the row first.
const confirmingRemove = ref(false)
const removeBusy = ref(false)
const removeError = ref('')

const removeMessage = computed(() => {
  const current = service.value
  if (!current) return ''
  if (current.status === 'proposed') {
    return `${current.name} was only a suggestion, so it is deleted for good. Nothing else is affected.`
  }
  return `${current.name} is taken off the site and stops being polled. Its history is kept, and it can be restored from the Removed filter.`
})

function cancelRemove() {
  confirmingRemove.value = false
  removeError.value = ''
}

async function removeService() {
  if (!service.value) return

  removeBusy.value = true
  removeError.value = ''
  try {
    await $fetch(`/api/v1/services/${props.slug}`, { method: 'DELETE' })
    confirmingRemove.value = false
    emit('removed')
  } catch (err) {
    removeError.value = apiError(err, 'Could not remove the service.')
  } finally {
    removeBusy.value = false
  }
}

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
              <p class="text-sm font-medium">
                {{ service.status === 'no_match' ? 'Profile detection' : 'Scheduled polling' }}
              </p>
              <p class="text-sm opacity-70">
                {{
                  service.status === 'no_match'
                    ? (service.paused
                      ? 'Paused. The page is not checked for a profile.'
                      : 'Trying to match a profile on its usual schedule.')
                    : (service.paused
                      ? 'Paused. The last known state stays on the page.'
                      : 'Running on its usual schedule.')
                }}
              </p>
            </div>
            <button
              type="button"
              class="btn btn-sm gap-2"
              :disabled="pausing || busy"
              :title="
                service.paused
                  ? (service.status === 'no_match'
                    ? 'Let the hourly detection attempts continue.'
                    : 'Start scheduled polls again and refresh its page.')
                  : (service.status === 'no_match'
                    ? 'Stop trying to match a profile for this page.'
                    : 'Stop scheduled polls and keep the last known state on its page.')
              "
              @click="togglePause"
            >
              <span v-if="pausing" class="loading loading-spinner loading-xs" />
              {{ service.paused ? 'Resume' : 'Pause' }}
              {{ service.status === 'no_match' ? 'detection' : 'polling' }}
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

          <div
            v-if="showRemove && isAdmin && service"
            class="mt-6 border-t border-base-300 pt-5"
          >
            <h3 class="text-sm font-semibold text-error">Remove service</h3>
            <p class="mt-1 text-sm opacity-70">
              Take {{ service.name }} off the site and stop checking it. Its
              history is kept, so it can be restored from the Removed filter.
            </p>
            <button
              type="button"
              class="btn btn-error btn-sm mt-3"
              :disabled="busy || pausing || removeBusy"
              @click="confirmingRemove = true"
            >
              Remove service
            </button>
          </div>
        </template>
      </div>
    </div>

    <ConfirmDialog
      v-if="confirmingRemove"
      heading="Remove this service?"
      :message="removeMessage"
      confirm-label="Remove service"
      :busy="removeBusy"
      :error="removeError"
      @cancel="cancelRemove"
      @confirm="removeService"
    />
  </div>
</template>
