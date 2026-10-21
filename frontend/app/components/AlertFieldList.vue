<script setup lang="ts">
import type { AlertField } from '~/types/api'

// A list of name and value pairs, which is how a webhook's headers are written.
// Rows are added and removed; a row left completely empty is dropped by the
// server, so a screen that leaves one behind is not an error.
//
// Adding and removing are links rather than buttons: a button's own look is its
// fill, and a ghost one has none until it is hovered, which never happens on a
// phone.
const props = withDefaults(
  defineProps<{
    label: string
    namePlaceholder: string
    valuePlaceholder: string
    addLabel: string
    // The heading is skipped where the list already sits under one, such as a
    // fieldset that names it. The label is still what the inputs are called.
    showLabel?: boolean
  }>(),
  { showLabel: true },
)

const fields = defineModel<AlertField[]>({ required: true })

function add() {
  fields.value = [...fields.value, { name: '', value: '' }]
}

function remove(index: number) {
  fields.value = fields.value.filter((_, i) => i !== index)
}
</script>

<template>
  <div>
    <p v-if="props.showLabel" class="text-sm font-medium">{{ props.label }}</p>

    <div v-for="(field, index) in fields" :key="index" class="mt-2 flex items-center gap-2">
      <!-- The name and the value are one control rather than two fields that
           happen to sit beside each other. -->
      <div class="join min-w-0 flex-1">
        <input
          v-model="field.name"
          type="text"
          :placeholder="props.namePlaceholder"
          :aria-label="`${props.label} name`"
          class="input input-bordered input-sm join-item min-w-0 flex-1"
        >
        <input
          v-model="field.value"
          type="text"
          :placeholder="props.valuePlaceholder"
          :aria-label="`${props.label} value`"
          class="input input-bordered input-sm join-item min-w-0 flex-[2]"
        >
      </div>
      <button
        type="button"
        class="link text-sm opacity-70"
        :aria-label="`Remove this ${props.label.toLowerCase()}`"
        @click="remove(index)"
      >
        ✕
      </button>
    </div>

    <button type="button" class="link mt-2 text-sm" @click="add">
      {{ props.addLabel }}
    </button>
  </div>
</template>
