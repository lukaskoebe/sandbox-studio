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
