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

const broadcast = reactive({ title: '', body: '' })
const sending = ref(false)
const sent = ref<number | null>(null)

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

async function sendBroadcast() {
  sending.value = true
  error.value = ''
  sent.value = null

  try {
    const res = await $fetch<{ sent: number }>('/api/v1/staff/notifications', {
      method: 'POST',
      body: { title: broadcast.title, body: broadcast.body },
    })
    sent.value = res.sent
    broadcast.title = ''
    broadcast.body = ''
  } catch (err) {
    error.value = apiError(err, 'Could not send the announcement.')
  } finally {
    sending.value = false
  }
}
</script>

<template>
  <div class="mx-auto max-w-3xl px-4 py-8">
    <header class="mb-6 flex flex-wrap items-end justify-between gap-3">
      <div>
        <h1 class="text-2xl font-bold tracking-tight">Notifications</h1>
        <p class="mt-1 text-sm opacity-70">
          Site announcements and operational notices, in one place.
        </p>
      </div>
      <button
        v-if="unread > 0"
        type="button"
        class="btn btn-ghost btn-sm"
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
        class="rounded-box border bg-base-100 p-4 shadow-sm"
        :class="item.readAt ? 'border-base-300' : 'border-primary/40'"
      >
        <div class="flex items-start gap-3">
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

    <section
      v-if="canManageServices"
      class="mt-10 rounded-box border border-base-300 bg-base-100 p-5 shadow-sm"
    >
      <h2 class="text-lg font-semibold">Send an announcement</h2>
      <p class="mt-1 text-sm opacity-70">
        One message for every subscriber account's notification feed. It is not
        sent by email or chat.
      </p>

      <form class="mt-4 flex flex-col gap-3" @submit.prevent="sendBroadcast">
        <label class="form-control">
          <span class="label-text mb-1">Title</span>
          <input
            v-model.trim="broadcast.title"
            type="text"
            class="input input-bordered w-full"
            maxlength="160"
            required
          />
        </label>
        <label class="form-control">
          <span class="label-text mb-1">Message</span>
          <textarea
            v-model.trim="broadcast.body"
            class="textarea textarea-bordered min-h-28 w-full"
            maxlength="4000"
            required
          />
        </label>
        <div class="flex flex-wrap items-center gap-3">
          <button type="submit" class="btn btn-primary btn-sm" :disabled="sending">
            <span v-if="sending" class="loading loading-spinner loading-xs" />
            Send to subscribers
          </button>
          <p v-if="sent !== null" role="status" class="text-sm text-success">
            {{
              sent === 0
                ? 'There are no subscriber accounts to send to.'
                : sent === 1
                  ? 'Sent to 1 subscriber.'
                  : `Sent to ${sent} subscribers.`
            }}
          </p>
        </div>
      </form>
    </section>
  </div>
</template>
