export function useNotifications() {
  const unread = useState<number>('notifications:unread', () => 0)

  // The badge is the only thing on most pages that needs the count, so this is
  // deliberately a small request rather than loading the whole feed.
  async function loadUnread() {
    try {
      const res = await $fetch<{ unread: number }>('/api/v1/notifications/unread')
      unread.value = res.unread
    } catch {
      // A notification count is not worth an error banner on every page.
      unread.value = 0
    }
  }

  function setUnread(value: number) {
    unread.value = Math.max(0, value)
  }

  return { unread, loadUnread, setUnread }
}
