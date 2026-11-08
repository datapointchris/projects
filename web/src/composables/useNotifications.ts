import { ref } from 'vue'

export interface Notification {
  id: number
  message: string
  category: 'success' | 'warning' | 'error'
  dismissTimer: ReturnType<typeof setTimeout> | null
}

const AUTO_DISMISS_MS = 10_000

let nextId = 0

const notifications = ref<Notification[]>([])

export function useNotifications() {
  function show(message: string, category: Notification['category'] = 'success') {
    const id = nextId++
    const notification: Notification = {
      id,
      message,
      category,
      dismissTimer: setTimeout(() => close(id), AUTO_DISMISS_MS),
    }
    notifications.value.push(notification)
  }

  function close(id: number) {
    const idx = notifications.value.findIndex((n) => n.id === id)
    if (idx !== -1) {
      const notification = notifications.value[idx]!
      if (notification.dismissTimer) {
        clearTimeout(notification.dismissTimer)
      }
      notifications.value.splice(idx, 1)
    }
  }

  function closeAll() {
    for (const n of notifications.value) {
      if (n.dismissTimer) {
        clearTimeout(n.dismissTimer)
      }
    }
    notifications.value.splice(0)
  }

  return {
    notifications,
    show,
    close,
    closeAll,
  }
}
