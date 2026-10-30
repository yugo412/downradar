<script setup lang="ts">
import type { FeedNotification } from '~/types/api'

definePageMeta({ middleware: 'authenticated', ssr: false })

useSeoMeta({ title: 'Notifications', robots: 'noindex, nofollow' })

const { canManageServices } = useAuth()
const { unread, setUnread } = useNotifications()

const notifications = ref<FeedNotification[]>([])
const loading = ref(true)
const error = ref('')
const notice = ref('')
const markingAll = ref(false)
const marking = ref<number | null>(null)
const deleting = ref<number | null>(null)

onMounted(load)

async function load() {
  try {
    const res = await $fetch<{ notifications: FeedNotification[]; unread: number }>(
      '/api/v1/notifications',
    )
    notifications.value = res.notifications
    setUnread(res.unread)
  } catch (err) {
    error.value = apiError(err, 'Could not load your notifications.')
  } finally {
    loading.value = false
  }
}

async function markRead(item: FeedNotification) {
  if (item.readAt || marking.value !== null) return

  marking.value = item.id
  try {
    await $fetch(`/api/v1/notifications/${item.id}`, {
      method: 'PATCH',
      body: { read: true },
    })
    item.readAt = new Date().toISOString()
    setUnread(unread.value - 1)
  } catch (err) {
    error.value = apiError(err, 'Could not mark the notification as read.')
  } finally {
    marking.value = null
  }
}

async function remove(item: FeedNotification) {
  if (deleting.value !== null) return

  deleting.value = item.id
  error.value = ''
  notice.value = ''
  try {
    await $fetch(`/api/v1/notifications/${item.id}`, { method: 'DELETE' })
    notifications.value = notifications.value.filter((entry) => entry.id !== item.id)
    if (!item.readAt) setUnread(unread.value - 1)
  } catch (err) {
    error.value = apiError(err, 'Could not delete the notification.')
  } finally {
    deleting.value = null
  }
}

async function markAllRead() {
  markingAll.value = true
  error.value = ''
  notice.value = ''

  try {
    const res = await $fetch<{ updated: number }>('/api/v1/notifications/read', {
      method: 'POST',
    })
    const now = new Date().toISOString()
    notifications.value = notifications.value.map((item) => ({
      ...item,
      readAt: item.readAt ?? now,
    }))
    setUnread(0)
    notice.value =
      res.updated === 0
        ? 'There was nothing unread.'
        : res.updated === 1
          ? '1 notification marked as read.'
          : `${res.updated} notifications marked as read.`
  } catch (err) {
    error.value = apiError(err, 'Could not mark your notifications as read.')
  } finally {
    markingAll.value = false
  }
}

function serviceLink(item: FeedNotification) {
  return item.serviceSlug ? `/${item.serviceSlug}` : ''
}

function alertLink(item: FeedNotification) {
  if (!item.alertId) return ''
  return canManageServices.value ? `/alerts/all#alert-${item.alertId}` : '/alerts'
}
</script>

<template>
  <div class="mx-auto max-w-6xl px-4 py-8">
    <header class="mb-6 flex flex-wrap items-end justify-between gap-3">
      <div>
        <h1 class="text-2xl font-bold tracking-tight">Notifications</h1>
        <p class="mt-1 text-sm opacity-70">
          Alert changes, site announcements, and operational notices, in one place.
        </p>
      </div>
      <button
        v-if="unread > 0"
        type="button"
        class="link text-sm disabled:opacity-50"
        :disabled="markingAll"
        @click="markAllRead"
      >
        <span v-if="markingAll" class="loading loading-spinner loading-xs" />
        Mark all as read
      </button>
    </header>

    <p v-if="error" role="alert" class="alert alert-error mb-6">
      <span>{{ error }}</span>
    </p>
    <p v-if="notice" role="status" class="alert alert-success mb-6">
      <span>{{ notice }}</span>
    </p>

    <div v-if="loading" class="flex justify-center py-16">
      <span class="loading loading-spinner" />
    </div>

    <p
      v-else-if="!notifications.length"
      class="rounded-box border border-base-300 bg-base-100 p-6 text-center text-sm opacity-70"
    >
      There are no notifications yet.
    </p>

    <ol v-else class="flex flex-col gap-3">
      <li
        v-for="item in notifications"
        :key="item.id"
        class="relative rounded-box border bg-base-100 p-4 shadow-sm"
        :class="item.readAt ? 'border-base-300' : 'border-primary/40'"
      >
        <button
          type="button"
          class="absolute right-2 top-2 grid h-6 w-6 place-items-center rounded-full border-0 bg-transparent p-0 text-base-content/60 transition-colors hover:bg-base-200 hover:text-base-content disabled:opacity-40"
          :aria-label="`Delete notification: ${item.title}`"
          :disabled="deleting === item.id"
          @click="remove(item)"
        >
          <span v-if="deleting === item.id" class="loading loading-spinner loading-xs" />
          <svg
            v-else
            class="h-4 w-4"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="2"
            stroke-linecap="round"
            aria-hidden="true"
          >
            <path d="M6 6l12 12M18 6L6 18" />
          </svg>
        </button>

        <div class="flex items-start gap-3 pr-8">
          <span
            class="mt-1.5 h-2 w-2 shrink-0 rounded-full"
            :class="item.readAt ? 'bg-base-300' : 'bg-primary'"
            aria-hidden="true"
          />
          <div class="min-w-0 flex-1">
            <div class="flex flex-wrap items-start justify-between gap-2">
              <h2 class="font-semibold">{{ item.title }}</h2>
              <LocalTime :at="item.createdAt" class="text-xs whitespace-nowrap opacity-60" />
            </div>
            <p class="mt-1 text-sm leading-relaxed opacity-80">{{ item.body }}</p>

            <div class="mt-3 flex flex-wrap items-center gap-x-4 gap-y-2 text-sm">
              <NuxtLink
                v-if="serviceLink(item)"
                :to="serviceLink(item)"
                class="link"
                @click="markRead(item)"
              >
                {{ item.serviceName || 'Open service' }}
              </NuxtLink>
              <NuxtLink
                v-if="alertLink(item)"
                :to="alertLink(item)"
                class="link"
                @click="markRead(item)"
              >
                View alert
              </NuxtLink>
              <button
                v-if="!item.readAt"
                type="button"
                class="link"
                :disabled="marking === item.id"
                @click="markRead(item)"
              >
                Mark as read
              </button>
            </div>
          </div>
        </div>
      </li>
    </ol>
  </div>
</template>
