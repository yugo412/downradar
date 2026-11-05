<script setup lang="ts">
import type { Team } from '~/types/api'

// The personal/team switch, built on the same lifted tabs as a vendor's detail
// page: the labels sit on the strip and the page is the content panel below.
// Choosing a workspace opens its page; the tab itself is the navigation.
const props = defineProps<{ teams: Team[]; current?: string | null }>()

const tabGroup = useId()

function select(slug: string | null) {
  if (slug === (props.current ?? null)) return
  void navigateTo(slug ? `/teams/${slug}` : '/teams')
}
</script>

<template>
  <LiftedTabs label="Workspace">
    <template #tabs>
      <label
        class="tab flex-none cursor-pointer whitespace-nowrap"
        :class="{ 'tab-active': !current }"
      >
        <input
          type="radio"
          class="sr-only"
          :name="tabGroup"
          :checked="!current"
          @change="select(null)"
        />
        Personal
      </label>
      <label
        v-for="team in teams"
        :key="team.id"
        class="tab flex-none cursor-pointer whitespace-nowrap"
        :class="{ 'tab-active': current === team.slug }"
      >
        <input
          type="radio"
          class="sr-only"
          :name="tabGroup"
          :checked="current === team.slug"
          @change="select(team.slug)"
        />
        {{ team.name }}
      </label>
    </template>

    <slot />
  </LiftedTabs>
</template>
