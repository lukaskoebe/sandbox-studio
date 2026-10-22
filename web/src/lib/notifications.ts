import { useSyncExternalStore } from "react"

export type Permission = NotificationPermission | "unsupported"

/** Whether the browser lets Studio notify, or can't at all. */
export function notificationPermission(): Permission {
  return typeof Notification === "undefined"
    ? "unsupported"
    : Notification.permission
}

const listeners = new Set<() => void>()
const changed = () => listeners.forEach((l) => l())

function subscribe(listener: () => void) {
  listeners.add(listener)
  // The permission also changes in the browser's site settings, outside the page.
  let status: PermissionStatus | undefined
  if ("permissions" in navigator)
    navigator.permissions
      .query({ name: "notifications" })
      .then((s) => {
        status = s
        s.addEventListener("change", changed)
      })
      .catch(() => {})
  return () => {
    listeners.delete(listener)
    status?.removeEventListener("change", changed)
  }
}

/**
 * The notification permission, shared by the approval stack, Settings → General and Home's
 * banner, so asking in one place updates the others.
 */
export function useNotificationPermission() {
  const permission = useSyncExternalStore(subscribe, notificationPermission)
  return {
    permission,
    request: () => {
      void Notification.requestPermission().then(changed)
    },
  }
}

/**
 * Whether Studio should notify now: the window is not the one in front. A tab in view behind
 * another app or on another monitor counts as visible, so visibility alone is not enough.
 */
export function shouldNotify() {
  return !document.hasFocus() && notificationPermission() === "granted"
}
