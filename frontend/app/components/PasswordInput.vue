<script setup lang="ts">
// A password field with a way to look at what was typed. A password typed twice
// by mistake is the usual reason a sign in fails, and the reset and register
// screens have no confirmation field to catch it, so being able to read it back
// matters more there than anywhere.
defineOptions({ inheritAttrs: false })

const model = defineModel<string>({ required: true })

// The label above the field names what is being typed, and the switch inside it
// says what it is showing: a password is the usual secret here, and another kind
// of one can name itself rather than being called a password.
defineProps<{ label: string; secret?: string }>()

const id = useId()
const shown = ref(false)
</script>

<template>
  <div class="form-control w-full">
    <label :for="id" class="label-text mb-1 cursor-pointer text-sm font-medium">{{ label }}</label>

    <div class="relative flex">
      <input
        :id="id"
        v-model="model"
        :type="shown ? 'text' : 'password'"
        class="input input-bordered w-full pr-12"
        v-bind="$attrs"
      />
      <button
        type="button"
        class="btn btn-ghost btn-xs absolute inset-y-0 right-1 my-auto"
        :aria-label="shown ? `Hide ${secret ?? 'password'}` : `Show ${secret ?? 'password'}`"
        :aria-pressed="shown"
        @click="shown = !shown"
      >
        <svg
          v-if="shown"
          class="h-4 w-4"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="2"
          stroke-linecap="round"
          stroke-linejoin="round"
          aria-hidden="true"
        >
          <path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94" />
          <path d="M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19" />
          <line x1="1" y1="1" x2="23" y2="23" />
        </svg>
        <svg
          v-else
          class="h-4 w-4"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="2"
          stroke-linecap="round"
          stroke-linejoin="round"
          aria-hidden="true"
        >
          <path d="M1 12s4-7 11-7 11 7 11 7-4 7-11 7-11-7-11-7z" />
          <circle cx="12" cy="12" r="3" />
        </svg>
      </button>
    </div>

    <slot />
  </div>
</template>
