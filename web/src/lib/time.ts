/**
 * One way to write a time, everywhere:
 * - ago: "just now", "5 min ago", "3 h ago", then a clock time ("14:02", "Mon 14:02", "21 Nov");
 * - lasted: "<1 min", "12 min", "1 h 20 min";
 * - left: "33 s left", "4 min left".
 * Clock times follow the browser's 12- or 24-hour setting.
 */

const clockFormat = new Intl.DateTimeFormat(undefined, {
  hour: "numeric",
  minute: "2-digit",
})
const weekdayFormat = new Intl.DateTimeFormat(undefined, { weekday: "short" })
const dayFormat = new Intl.DateTimeFormat(undefined, {
  day: "numeric",
  month: "short",
})
const yearFormat = new Intl.DateTimeFormat(undefined, {
  day: "numeric",
  month: "short",
  year: "numeric",
})

const toDate = (at: string | number | Date) =>
  at instanceof Date ? at : new Date(at)

/** The time of day: "14:02", or "2:02 PM" where the browser counts 12 hours. */
export function clock(at: string | number | Date): string {
  return clockFormat.format(toDate(at))
}

/** When something was, by the clock: the time today, the weekday and time this week, else the day. */
export function when(at: string | number | Date, now = Date.now()): string {
  const d = toDate(at)
  const today = new Date(now)
  if (d.toDateString() === today.toDateString()) return clock(d)
  const days = (now - d.getTime()) / 86_400_000
  if (days > 0 && days < 6) return `${weekdayFormat.format(d)} ${clock(d)}`
  if (d.getFullYear() === today.getFullYear()) return dayFormat.format(d)
  return yearFormat.format(d)
}

/** How long ago: "just now", "5 min ago", "3 h ago", and past 12 hours the clock time (`when`). */
export function ago(at: string | number | Date, now = Date.now()): string {
  const d = toDate(at)
  const seconds = Math.floor((now - d.getTime()) / 1000)
  if (seconds < 60) return "just now"
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes} min ago`
  const hours = Math.floor(minutes / 60)
  if (hours < 12) return `${hours} h ago`
  return when(d, now)
}

/** How long something lasted, from seconds: "<1 min", "12 min", "1 h 20 min". */
export function lasted(seconds: number): string {
  const h = Math.floor(seconds / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  if (h > 0) return m > 0 ? `${h} h ${m} min` : `${h} h`
  if (m > 0) return `${m} min`
  return "<1 min"
}

/** How long is left, from seconds: "33 s left", "4 min left". */
export function left(seconds: number): string {
  if (seconds < 60) return `${Math.max(0, Math.ceil(seconds))} s left`
  return `${Math.round(seconds / 60)} min left`
}
