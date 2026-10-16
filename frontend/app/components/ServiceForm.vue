<script setup lang="ts">
import type {
  AdminProfile,
  AdminService,
  ServicePayload,
  ServiceStatus,
  Visibility,
} from '~/types/api'

const props = withDefaults(
  defineProps<{
    initial?: AdminService | null
    profiles: AdminProfile[]
    categories?: string[]
    submitLabel?: string
    busy?: boolean
    error?: string
    showStatus?: boolean
    // Only staff may move a service between the catalogue and their own list.
    canChooseVisibility?: boolean
  }>(),
  {
    initial: null,
    categories: () => [],
    submitLabel: 'Save',
    busy: false,
    error: '',
    showStatus: false,
    canChooseVisibility: false,
  },
)

const emit = defineEmits<{
  submit: [payload: ServicePayload]
  cancel: []
}>()

// A datalist needs an id, and there is no reason to assume only one form is on
// the page.
const categoryListId = useId()

const name = ref(props.initial?.name ?? '')
const url = ref(props.initial?.url ?? '')
const profile = ref(props.initial?.profile ?? '')
const category = ref(props.initial?.category ?? '')
const logoUrl = ref(props.initial?.logoUrl ?? '')
const aliases = ref((props.initial?.aliases ?? []).join(', '))
const status = ref<ServiceStatus>(props.initial?.status ?? 'published')
const visibility = ref<Visibility>(props.initial?.ownerId ? 'private' : 'public')

// A vendor is only polled when it has both a url and a profile, so the form
// insists on them for anything that is about to be published.
const publishing = computed(() => status.value === 'published')

function onSubmit() {
  emit('submit', {
    name: name.value,
    url: url.value,
    profile: profile.value,
    category: category.value,
    logoUrl: logoUrl.value,
    aliases: aliases.value
      .split(',')
      .map((alias) => alias.trim())
      .filter(Boolean),
    ...(props.showStatus ? { status: status.value } : {}),
    // A caller who cannot choose is always private, and leaving the field out
    // is what tells the API to leave the ownership alone.
    ...(props.canChooseVisibility ? { visibility: visibility.value } : {}),
  })
}
</script>

<template>
  <form class="flex flex-col gap-4" @submit.prevent="onSubmit">
    <label class="form-control w-full">
      <span class="label-text mb-1 text-sm font-medium">Service name</span>
      <input
        v-model="name"
        type="text"
        class="input input-bordered w-full"
        placeholder="e.g. Some Service"
        maxlength="80"
        required
      />
    </label>

    <label class="form-control w-full">
      <span class="label-text mb-1 text-sm font-medium">Status page URL</span>
      <input
        v-model="url"
        type="url"
        class="input input-bordered w-full"
        placeholder="https://status.example.com"
        maxlength="300"
        :required="publishing"
      />
      <span class="label-text-alt mt-1 opacity-60">
        The official status page, not the vendor's home page.
      </span>
    </label>

    <label class="form-control w-full">
      <span class="label-text mb-1 text-sm font-medium">Adapter profile</span>
      <select v-model="profile" class="select select-bordered w-full" :required="publishing">
        <option value="">Not chosen yet</option>
        <option v-for="p in profiles" :key="p.name" :value="p.name">
          {{ p.name }} ({{ p.kind }})
        </option>
      </select>
      <span class="label-text-alt mt-1 opacity-60">
        Says how the status page is read. A published service without one is never polled.
      </span>
    </label>

    <div class="grid gap-4 sm:grid-cols-2">
      <label class="form-control w-full">
        <span class="label-text mb-1 text-sm font-medium">Category</span>
        <input
          v-model="category"
          type="text"
          class="input input-bordered w-full"
          placeholder="e.g. developer"
          maxlength="40"
          :list="categoryListId"
        />
        <datalist :id="categoryListId">
          <option v-for="c in categories" :key="c" :value="c" />
        </datalist>
        <span class="label-text-alt mt-1 opacity-60">
          Pick one that is already used, or type a new one.
        </span>
      </label>

      <label class="form-control w-full">
        <span class="label-text mb-1 text-sm font-medium">Logo URL</span>
        <input
          v-model="logoUrl"
          type="url"
          class="input input-bordered w-full"
          placeholder="https://example.com/logo.png"
          maxlength="500"
        />
      </label>
    </div>

    <label class="form-control w-full">
      <span class="label-text mb-1 text-sm font-medium">Search aliases</span>
      <input
        v-model="aliases"
        type="text"
        class="input input-bordered w-full"
        placeholder="some service down, some service status"
      />
      <span class="label-text-alt mt-1 opacity-60">Comma separated. Used by the site search.</span>
    </label>

    <label v-if="canChooseVisibility" class="form-control w-full">
      <span class="label-text mb-1 text-sm font-medium">Visibility</span>
      <select v-model="visibility" class="select select-bordered w-full">
        <option value="public">Public — listed on the site for everyone</option>
        <option value="private">Private — polled, but only you can see it</option>
      </select>
      <span class="label-text-alt mt-1 opacity-60">
        A private service is still checked, it just stays out of the catalogue.
      </span>
    </label>

    <label v-if="showStatus" class="form-control w-full">
      <span class="label-text mb-1 text-sm font-medium">Status</span>
      <select v-model="status" class="select select-bordered w-full">
        <option value="proposed">Proposed — waiting for a review</option>
        <option value="published">Published — live and polled</option>
        <option value="rejected">Rejected — kept for the record, never polled</option>
      </select>
    </label>

    <p v-if="error" role="alert" class="text-sm text-error">{{ error }}</p>

    <div class="flex items-center justify-end gap-2 pt-1">
      <button type="button" class="btn btn-ghost btn-sm" :disabled="busy" @click="emit('cancel')">
        Cancel
      </button>
      <button type="submit" class="btn btn-primary btn-sm" :disabled="busy">
        <span v-if="busy" class="loading loading-spinner loading-xs" />
        {{ submitLabel }}
      </button>
    </div>
  </form>
</template>
