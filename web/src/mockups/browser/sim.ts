import { useEffect, useReducer } from "react"
import {
  blankLogin,
  earlierDownloads,
  script,
  sites,
  stepTime,
  type ActionsPolicy,
  type Ask,
  type Download,
  type View,
} from "./data"

/** The states the mockup's switcher starts from. */
export const presets = ["asleep", "agent", "asks", "you"] as const
export type Preset = (typeof presets)[number]

export const presetNames: Record<Preset, string> = {
  asleep: "Asleep",
  agent: "Finn browsing",
  asks: "Needs you",
  you: "You drive",
}

export type LogEntry = {
  id: number
  actor: "agent" | "you"
  text: string
  time: string
  outcome: "ok" | "waiting" | "allowed" | "denied"
  /** The page after the entry, for replay. */
  view: View
  ask?: Ask
}

export type Sim = {
  vm: "asleep" | "waking" | "running"
  driver: "agent" | "you"
  /** Finn has a task that uses the browser. */
  working: boolean
  /** The next step of the script; it waits on an approval while `pending`. */
  next: number
  pending: boolean
  view: View
  back: View[]
  forward: View[]
  log: LogEntry[]
  /** The log entry shown instead of the live page. */
  replay: number | null
  tab: "profile" | "throwaway"
  throwawayOpen: boolean
  downloads: Download[]
  allowedHosts: string[]
  policies: Record<string, ActionsPolicy>
  /** A one-line note the mockup shows briefly, like a toast. */
  note?: string
}

export type SimAction =
  | { type: "tick" }
  | { type: "decide"; how: "allow" | "always" | "deny" }
  | { type: "wheel"; to: "agent" | "you" }
  | { type: "navigate"; url: string }
  | { type: "history"; dir: "back" | "forward" }
  | { type: "reload" }
  | { type: "wake" }
  | { type: "awake" }
  | { type: "sleep" }
  | { type: "replay"; entry: number | null }
  | { type: "tab"; tab: Sim["tab"] }
  | { type: "closeThrowaway" }
  | { type: "send"; file: string; sandbox: string }
  | { type: "policy"; host: string; policy: ActionsPolicy }
  | { type: "allowHeld"; always: boolean }
  | { type: "denyHeld" }
  | { type: "target"; target: string }
  | { type: "reset" }
  | { type: "note"; text?: string }

/** Runs the script up to `upTo`, as if every approval had been given. */
function played(upTo: number): Pick<Sim, "view" | "log" | "back"> {
  let view: View = blankLogin
  const back: View[] = []
  const log: LogEntry[] = []
  for (let i = 0; i < upTo; i++) {
    const step = script[i]
    const after = step.apply(view)
    if (after.url !== view.url && i > 0) back.push(view)
    view = after
    log.push({
      id: i,
      actor: "agent",
      text: step.text,
      time: stepTime(i),
      outcome: step.ask ? "allowed" : "ok",
      view,
      ask: step.ask,
    })
  }
  return { view, log, back }
}

export function initialSim(preset: Preset): Sim {
  const base: Sim = {
    vm: "running",
    driver: "agent",
    working: true,
    next: 0,
    pending: false,
    view: blankLogin,
    back: [],
    forward: [],
    log: [],
    replay: null,
    tab: "profile",
    throwawayOpen: true,
    downloads: earlierDownloads,
    allowedHosts: ["console.pebble.dev", "pebble.dev", "github.com"],
    policies: Object.fromEntries(sites.map((s) => [s.host, s.actions])),
  }
  switch (preset) {
    case "asleep":
      return {
        ...base,
        ...played(5),
        log: [],
        back: [],
        vm: "asleep",
        working: false,
        next: script.length,
        throwawayOpen: false,
      }
    case "agent":
      return { ...base, ...played(3), next: 3 }
    case "asks":
      return withPending({ ...base, ...played(5), next: 5 })
    case "you":
      return { ...base, ...played(5), next: 5, driver: "you" }
  }
}

function withPending(s: Sim): Sim {
  const step = script[s.next]
  return {
    ...s,
    pending: true,
    log: [
      ...s.log,
      {
        id: s.log.length,
        actor: "agent",
        text: step.text.replace(/^(\w+)ed\b/, (w) => `Wants to ${verb(w)}`),
        time: stepTime(s.log.length),
        outcome: "waiting",
        view: s.view,
        ask: step.ask,
      },
    ],
  }
}

// "Clicked" → "click", for the line a waiting step shows.
function verb(past: string) {
  const v = past.toLowerCase()
  if (v === "filled") return "fill"
  if (v === "downloaded") return "download"
  if (v === "clicked") return "click"
  return v.replace(/ed$/, "")
}

function go(s: Sim, view: View): Sim {
  if (view.url === s.view.url) return { ...s, view }
  return { ...s, view, back: [...s.back, s.view], forward: [] }
}

function apply(s: Sim, i: number, outcome: LogEntry["outcome"]): Sim {
  const step = script[i]
  let next = go(s, step.apply(s.view))
  if (step.ask?.kind === "download")
    next = {
      ...next,
      downloads: [
        { name: step.ask.file, size: step.ask.size, when: "just now" },
        ...next.downloads,
      ],
      note: `Saved ${step.ask.file} in Finn's downloads`,
    }
  const log = s.pending
    ? next.log.map((e, j) =>
        j === next.log.length - 1
          ? { ...e, text: step.text, outcome, view: next.view }
          : e
      )
    : [
        ...next.log,
        {
          id: next.log.length,
          actor: "agent" as const,
          text: step.text,
          time: stepTime(s.log.length),
          outcome,
          view: next.view,
          ask: step.ask,
        },
      ]
  return { ...next, log, next: i + 1, pending: false }
}

function logYou(s: Sim, text: string): Sim {
  return {
    ...s,
    log: [
      ...s.log,
      {
        id: s.log.length,
        actor: "you",
        text,
        time: stepTime(s.log.length),
        outcome: "ok",
        view: s.view,
      },
    ],
  }
}

function host(url: string) {
  try {
    return new URL(url).host
  } catch {
    return ""
  }
}

/** The page an address shows: the fake site's pages, the hold page, or any other site. */
function viewFor(s: Sim, raw: string): View {
  const url = /^[a-z]+:\/\//i.test(raw) ? raw : `https://${raw}`
  const h = host(url)
  if (h === "console.pebble.dev") {
    const path = new URL(url).pathname
    const page =
      path.startsWith("/webhooks") && s.view.page !== "login"
        ? "webhooks"
        : path.startsWith("/overview") && s.view.page !== "login"
          ? "overview"
          : s.view.page === "login"
            ? "login"
            : "overview"
    return { ...s.view, page, url: `https://${h}/${page}` }
  }
  if (h === "pebble.dev") return { page: "changelog", url }
  if (s.allowedHosts.includes(h)) return { page: "other", url }
  return { page: "held", url }
}

function reduce(s: Sim, a: SimAction): Sim {
  switch (a.type) {
    case "tick": {
      if (s.vm !== "running" || s.driver !== "agent" || !s.working || s.pending)
        return s
      if (s.next >= script.length) return { ...s, working: false }
      const step = script[s.next]
      const policy = s.policies["console.pebble.dev"]
      if (step.ask?.kind === "action" && policy === "allow")
        return apply(s, s.next, "ok")
      if (step.ask?.kind === "action" && policy === "block")
        return {
          ...s,
          next: s.next + 1,
          log: [
            ...s.log,
            {
              id: s.log.length,
              actor: "agent",
              text: `${step.text}: blocked by the site's rule`,
              time: stepTime(s.log.length),
              outcome: "denied",
              view: s.view,
            },
          ],
        }
      if (step.ask) return withPending(s)
      return apply(s, s.next, "ok")
    }
    case "decide": {
      if (!s.pending) return s
      if (a.how === "deny") {
        const log = s.log.map((e, j) =>
          j === s.log.length - 1 ? { ...e, outcome: "denied" as const } : e
        )
        return { ...s, log, pending: false, next: s.next + 1 }
      }
      const step = script[s.next]
      const policies =
        a.how === "always" && step.ask?.kind === "action"
          ? { ...s.policies, "console.pebble.dev": "allow" as const }
          : s.policies
      return apply({ ...s, policies }, s.next, "allowed")
    }
    case "wheel":
      if (a.to === s.driver) return s
      return logYou(
        { ...s, driver: a.to, replay: null },
        a.to === "you" ? "Took the wheel" : "Handed the wheel back to Finn"
      )
    case "navigate": {
      const taken =
        s.driver === "agent"
          ? logYou({ ...s, driver: "you" }, "Took the wheel")
          : s
      const view = viewFor(taken, a.url)
      return logYou(
        { ...go(taken, view), replay: null, tab: "profile" },
        `Opened ${host(view.url) || a.url}`
      )
    }
    case "history": {
      const from = a.dir === "back" ? s.back : s.forward
      const view = from.at(-1)
      if (!view) return s
      return a.dir === "back"
        ? {
            ...s,
            view,
            back: s.back.slice(0, -1),
            forward: [...s.forward, s.view],
          }
        : {
            ...s,
            view,
            forward: s.forward.slice(0, -1),
            back: [...s.back, s.view],
          }
    }
    case "reload":
      return { ...s, note: "Reloaded" }
    case "wake":
      return { ...s, vm: "waking" }
    case "awake":
      return { ...s, vm: "running" }
    case "sleep":
      return {
        ...s,
        vm: "asleep",
        working: false,
        pending: false,
        replay: null,
        throwawayOpen: false,
        tab: "profile",
        driver: "agent",
      }
    case "replay":
      return { ...s, replay: a.entry }
    case "tab":
      return { ...s, tab: a.tab, replay: null }
    case "closeThrowaway":
      return {
        ...s,
        throwawayOpen: false,
        tab: "profile",
        note: "Ended the throwaway session: its cookies are gone",
      }
    case "send":
      return {
        ...s,
        downloads: s.downloads.map((d) =>
          d.name === a.file ? { ...d, sentTo: a.sandbox } : d
        ),
        note: `Copied ${a.file} to ${a.sandbox}:/workspace/downloads`,
      }
    case "policy":
      return { ...s, policies: { ...s.policies, [a.host]: a.policy } }
    case "allowHeld": {
      const h = host(s.view.url)
      return logYou(
        {
          ...s,
          allowedHosts: [...s.allowedHosts, h],
          view: { page: "other", url: s.view.url },
          note: a.always
            ? `Added a rule: allow ${h} for Finn`
            : `Allowed ${h} until the browser sleeps`,
        },
        `Allowed ${h}`
      )
    }
    case "denyHeld": {
      const denied = logYou(
        { ...s, note: "Denied: the site stays held" },
        `Denied ${host(s.view.url)}`
      )
      return s.back.length
        ? reduce(denied, { type: "history", dir: "back" })
        : denied
    }
    case "target": {
      if (s.driver !== "you") return s
      const v = s.view
      switch (a.target) {
        case "signin":
          return logYou(
            go(s, {
              page: "overview",
              url: "https://console.pebble.dev/overview",
            }),
            "Clicked “Sign in”"
          )
        case "nav-overview":
          return logYou(
            go(s, {
              page: "overview",
              url: "https://console.pebble.dev/overview",
            }),
            "Clicked “Overview”"
          )
        case "nav-webhooks":
          return logYou(
            go(s, {
              page: "webhooks",
              url: "https://console.pebble.dev/webhooks",
            }),
            "Clicked “Webhooks”"
          )
        case "test-staging":
          return logYou(
            { ...s, view: { ...v, testSent: true } },
            "Clicked “Send test event”"
          )
        case "export":
          return logYou(
            {
              ...s,
              downloads: [
                { name: "events-staging.csv", size: "18 KB", when: "just now" },
                ...s.downloads.filter((d) => d.name !== "events-staging.csv"),
              ],
              note: "Saved events-staging.csv in Finn's downloads",
            },
            "Downloaded events-staging.csv"
          )
        case "email":
          return { ...s, view: { ...v, email: true } }
        case "password":
          return { ...s, view: { ...v, password: true } }
      }
      return s
    }
    case "reset":
      return {
        ...s,
        view: blankLogin,
        back: [],
        forward: [],
        note: "Reset the profile: Finn is signed out everywhere",
      }
    case "note":
      return { ...s, note: a.text }
  }
}

/** The mockup's browser: Finn plays the script while it drives; everything else is local. */
export function useBrowserSim(preset: Preset) {
  const [sim, dispatch] = useReducer(reduce, preset, initialSim)

  useEffect(() => {
    if (sim.vm !== "waking") return
    const t = setTimeout(() => dispatch({ type: "awake" }), 1400)
    return () => clearTimeout(t)
  }, [sim.vm])

  useEffect(() => {
    if (
      sim.vm !== "running" ||
      sim.driver !== "agent" ||
      !sim.working ||
      sim.pending
    )
      return
    const t = setTimeout(() => dispatch({ type: "tick" }), 2600)
    return () => clearTimeout(t)
  }, [sim.vm, sim.driver, sim.working, sim.pending, sim.next])

  useEffect(() => {
    if (!sim.note) return
    const t = setTimeout(() => dispatch({ type: "note" }), 3200)
    return () => clearTimeout(t)
  }, [sim.note])

  return [sim, dispatch] as const
}

/** The step Finn is on or waiting at: where its cursor points. */
export function agentTarget(sim: Sim) {
  if (sim.driver !== "agent" || !sim.working || sim.vm !== "running")
    return undefined
  return script[sim.next]?.target
}
