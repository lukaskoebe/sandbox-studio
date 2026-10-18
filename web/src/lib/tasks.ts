import type { components } from "@/lib/api/schema.gen"

export type Task = components["schemas"]["Task"]
export type TaskInput = components["schemas"]["TaskInput"]
export type Beat = components["schemas"]["Beat"]
export type Heartbeat = components["schemas"]["Heartbeat"]

/** The schedule a teammate starts with when its check-ins are turned on. */
export const defaultHeartbeat: Heartbeat = {
  everyHours: 4,
  fromHour: 9,
  toHour: 18,
}

export const taskStatusLabels: Record<Task["status"], string> = {
  open: "Open",
  in_progress: "In progress",
  done: "Done",
}

/** Task details are markdown, at most 8 KiB, as the server enforces. */
export const maxTaskDetailsBytes = 8 << 10

/** A local hour as the schedule words it: 9 is "09:00", and the window may end at 24:00. */
export function hourLabel(hour: number) {
  return `${String(hour).padStart(2, "0")}:00`
}

/** A check-in schedule as a sentence, such as "Every 4 hours, 09:00 to 18:00 local time". */
export function heartbeatSentence(h: Heartbeat) {
  const every = h.everyHours === 1 ? "hour" : "hours"
  return `Every ${h.everyHours} ${every}, ${hourLabel(h.fromHour)} to ${hourLabel(h.toHour)} local time`
}

/** A logged check-in as the activity list words it. */
export function beatLabel(b: Beat) {
  return b.outcome === "started"
    ? "Started a heartbeat chat"
    : `Skipped: ${b.reason}`
}

export type TaskSchedule = components["schemas"]["TaskSchedule"]
export type TaskRun = components["schemas"]["TaskRun"]
export type Weekday = NonNullable<TaskSchedule["days"]>[number]

/** The weekdays in the order the week shows them, with their short names. */
export const weekdays: [Weekday, string][] = [
  ["mon", "Mon"],
  ["tue", "Tue"],
  ["wed", "Wed"],
  ["thu", "Thu"],
  ["fri", "Fri"],
  ["sat", "Sat"],
  ["sun", "Sun"],
]

/** A day and time as a run's chat names it, such as "Fri 24 Oct 14:00", in local time. */
export function runTime(at: string | Date) {
  const d = typeof at === "string" ? new Date(at) : at
  const day = d.toLocaleDateString("en-GB", {
    weekday: "short",
    day: "numeric",
    month: "short",
  })
  const time = d.toLocaleTimeString("en-GB", {
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  })
  return `${day.replace(",", "")} ${time}`
}

/** The days of a schedule as words: "weekday", "day", "weekend day" or "Mon, Wed and Fri". */
function daysLabel(days: Weekday[]) {
  const set = new Set(days)
  const has = (list: Weekday[]) =>
    list.length === set.size && list.every((d) => set.has(d))
  if (set.size === 7) return "day"
  if (has(["mon", "tue", "wed", "thu", "fri"])) return "weekday"
  if (has(["sat", "sun"])) return "weekend day"
  const names = weekdays.filter(([d]) => set.has(d)).map(([, name]) => name)
  return names.length === 1
    ? names[0]
    : `${names.slice(0, -1).join(", ")} and ${names.at(-1)}`
}

/**
 * A task's schedule as a sentence: "Every weekday at 09:00", "Once, Fri 24 Oct at 14:00",
 * "Every 4 hours, 08:00–20:00".
 */
export function scheduleSentence(s: TaskSchedule) {
  switch (s.repeat) {
    case "once": {
      const at = new Date(`${s.date}T${s.time}`)
      const day = Number.isNaN(at.getTime())
        ? `${s.date}`
        : runTime(at).split(" ").slice(0, 3).join(" ")
      return `Once, ${day} at ${s.time}`
    }
    case "days":
      return `Every ${daysLabel(s.days ?? [])} at ${s.time}`
    case "hours": {
      const every = s.everyHours === 1 ? "hour" : `${s.everyHours} hours`
      return `Every ${every}, ${hourLabel(s.fromHour ?? 0)}–${hourLabel(s.toHour ?? 24)}`
    }
  }
}

/** A logged run as the activity list words it, after the task's title. */
export function runLabel(r: TaskRun, title: string) {
  switch (r.outcome) {
    case "started":
      return `Ran “${title}”`
    case "done":
      return `Ran “${title}”, done`
    case "skipped":
      return `Skipped the run of “${title}”: ${r.reason}`
    case "missed":
      return `Missed the run of “${title}”${r.dueAt ? ` at ${runTime(r.dueAt)}` : ""}: ${r.reason}`
  }
}
