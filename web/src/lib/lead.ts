import type { components } from "@/lib/api/schema.gen"
import { clock as clockTime, lasted } from "@/lib/time"

export type LeadView = components["schemas"]["LeadView"]
export type LeadDay = components["schemas"]["LeadDayView"]
export type LeadFacts = components["schemas"]["LeadFacts"]
export type LeadSettings = components["schemas"]["LeadSettings"]
export type LeadTeammate = components["schemas"]["LeadTeammate"]
export type LeadThread = components["schemas"]["LeadThread"]
export type LeadPush = components["schemas"]["LeadPush"]
export type LeadWaiting = components["schemas"]["LeadWaiting"]
export type LeadAsk = components["schemas"]["LeadAskView"]
export type LeadSince = components["schemas"]["LeadSince"]
export type LeadPlan = components["schemas"]["LeadPlanView"]
export type LeadPlanItem = components["schemas"]["LeadPlanItem"]
export type PlanStep = components["schemas"]["PlanStep"]
export type PlanPayload = components["schemas"]["PlanPayload"]
export type LeadOrder = components["schemas"]["OrderView"]
export type OrderInput = components["schemas"]["OrderInput"]

/** Whether a plan can still change: it isn't done, cancelled or failed. */
export function planOpen(p: { state: LeadPlan["state"] }): boolean {
  return p.state !== "done" && p.state !== "cancelled" && p.state !== "failed"
}

/** A plan's state in a few words, for its page and the sidebar. */
export function planStateLabel(state: LeadPlan["state"]): string {
  switch (state) {
    case "drafting":
      return "Drafting"
    case "asking":
      return "Your turn"
    case "proposed":
      return "Waits for approval"
    case "running":
      return "Running"
    case "done":
      return "Done"
    case "cancelled":
      return "Cancelled"
    case "failed":
      return "Failed"
  }
}

/** The steps that send something: messages and new threads, not the wait and the report. */
export function sendingSteps(steps: PlanStep[] | null | undefined): PlanStep[] {
  return (steps ?? []).filter((s) => s.kind === "message" || s.kind === "start")
}

/** Today's date as the API names days: YYYY-MM-DD, local. */
export function today(now = new Date()): string {
  const pad = (n: number) => String(n).padStart(2, "0")
  return `${now.getFullYear()}-${pad(now.getMonth() + 1)}-${pad(now.getDate())}`
}

/**
 * Studio's today, which Lead's days are named by: the browser's own date can be another, in
 * another time zone. The browser's until Lead has loaded.
 */
export function leadToday(v?: { today: { day: string } }): string {
  return v?.today.day ?? today()
}

/** A YYYY-MM-DD date as local midnight. */
export function dayDate(day: string): Date {
  const [y, m, d] = day.split("-").map(Number)
  return new Date(y, m - 1, d)
}

/**
 * "Thursday 19 November", or "Today" and "Yesterday" where short is asked for, by Studio's
 * today where it is given.
 */
export function dayName(
  day: string,
  opts: { short?: boolean; today?: string } = {}
): string {
  if (opts.short) {
    const t = opts.today ?? today()
    if (day === t) return "Today"
    const y = dayDate(t)
    y.setDate(y.getDate() - 1)
    if (day === today(y)) return "Yesterday"
    return dayDate(day).toLocaleDateString(undefined, {
      weekday: "short",
      day: "numeric",
      month: "short",
    })
  }
  return dayDate(day).toLocaleDateString(undefined, {
    weekday: "long",
    day: "numeric",
    month: "long",
  })
}

/** "14:05", local. */
export function clock(at: string): string {
  return clockTime(at)
}

/** USD × 10^6 as "$3.62"; a cost under a cent as "<$0.01". */
export function dollars(micros: number): string {
  if (micros > 0 && micros < 5000) return "<$0.01"
  return `$${(micros / 1e6).toFixed(2)}`
}

/** Seconds of turns as "2 h 50 min", "47 min" or "<1 min". */
export function turnTime(sec: number): string {
  return lasted(sec)
}

export function plural(n: number, one: string, many: string): string {
  return `${n} ${n === 1 ? one : many}`
}

/** A piece of the model's text: plain words, or a ref such as push:2 it points at. */
export type Piece = { text: string } | { ref: string }

const refToken = /\[([a-z]+:\d+)\]/g

/** Splits the model's text at its refs. */
export function pieces(text: string): Piece[] {
  const out: Piece[] = []
  let at = 0
  for (const m of text.matchAll(refToken)) {
    if (m.index > at) out.push({ text: text.slice(at, m.index) })
    out.push({ ref: m[1] })
    at = m.index + m[0].length
  }
  if (at < text.length) out.push({ text: text.slice(at) })
  return out
}

/** What a ref stands for in a day's facts. */
export type RefTarget =
  | { kind: "teammate"; teammate: LeadTeammate }
  | { kind: "thread"; thread: LeadThread; teammate: LeadTeammate }
  | { kind: "push"; push: LeadPush }
  | { kind: "approval"; waiting: LeadWaiting }
  | { kind: "task"; title: string; teammate: LeadTeammate }

/** Every ref of a day's facts, with what it stands for. */
export function refTargets(f: LeadFacts): Map<string, RefTarget> {
  const out = new Map<string, RefTarget>()
  for (const t of f.teammates ?? []) {
    out.set(t.ref, { kind: "teammate", teammate: t })
    for (const th of t.threads ?? [])
      out.set(th.ref, { kind: "thread", thread: th, teammate: t })
    for (const r of t.tasks ?? [])
      out.set(r.ref, { kind: "task", title: r.title, teammate: t })
  }
  for (const p of f.pushes ?? []) out.set(p.ref, { kind: "push", push: p })
  for (const w of f.waiting ?? [])
    out.set(w.ref, { kind: "approval", waiting: w })
  return out
}

/** Lead's weekday bits: Monday is bit 0. */
export const weekdays = ["Mo", "Tu", "We", "Th", "Fr", "Sa", "Su"] as const

/** Weekday bits in words: "Every day", "Weekdays", "Weekends" or "Mo, We, Fr". */
export function daysLabel(days: number): string {
  if ((days & 0b1111111) === 0b1111111) return "Every day"
  if ((days & 0b1111111) === 0b0011111) return "Weekdays"
  if ((days & 0b1111111) === 0b1100000) return "Weekends"
  return weekdays.filter((_, i) => days & (1 << i)).join(", ")
}

/** Minutes after midnight as "09:00". */
export function hhmm(min: number): string {
  return `${String(Math.floor(min / 60)).padStart(2, "0")}:${String(min % 60).padStart(2, "0")}`
}

/** The model's text with its refs written out as names, for a plan's first message. */
export function plainText(text: string, refs: Map<string, RefTarget>): string {
  return pieces(text)
    .map((p) => {
      if ("text" in p) return p.text
      const r = refs.get(p.ref)
      switch (r?.kind) {
        case "teammate":
          return r.teammate.name
        case "thread":
          return `“${r.thread.title}”`
        case "push":
          return r.push.prNumber ? `#${r.push.prNumber}` : r.push.branch
        case "approval":
          return r.waiting.subject
        case "task":
          return r.title
      }
      return ""
    })
    .join("")
}
