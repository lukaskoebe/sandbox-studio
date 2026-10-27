import { createCn } from "cn/config"

/**
 * Joins class names and drops the ones a later class overrides. It knows the type scale's own
 * sizes (styles.css), which would otherwise pass for text colors and lose to text-foreground.
 */
export const cn = createCn({
  extend: { theme: { text: ["title", "caption", "meta"] } },
})

/** A byte count for people: 980 B, 12 KB, 3.4 MB. */
export function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  const units = ["KB", "MB", "GB", "TB"]
  let value = bytes / 1024
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024
    unit++
  }
  return `${value < 10 ? value.toFixed(1) : Math.round(value)} ${units[unit]}`
}

/** host:port, with brackets around an IPv6 host: [2606:4700::1]:443. */
export function hostPort(host: string, port: number): string {
  if (!port) return host // a lookup refused before any connection has no port
  return host.includes(":") ? `[${host}]:${port}` : `${host}:${port}`
}

/** How long ago a timestamp was: "just now", "5m ago", "3h ago". */
export function formatAge(iso: string, now = Date.now()): string {
  const seconds = Math.floor((now - Date.parse(iso)) / 1000)
  if (seconds < 5) return "just now"
  if (seconds < 60) return `${seconds}s ago`
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes}m ago`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h ago`
  return `${Math.floor(hours / 24)}d ago`
}

/** How long a sandbox's VM has been up since it started: "Up 14 min", "Up 2 h", "Up 3 d". */
export function formatUptime(iso: string, now = Date.now()): string {
  const minutes = Math.max(0, Math.floor((now - Date.parse(iso)) / 60_000))
  if (minutes < 60) return `Up ${minutes} min`
  const hours = Math.floor(minutes / 60)
  if (hours < 48) return `Up ${hours} h`
  return `Up ${Math.floor(hours / 24)} d`
}
