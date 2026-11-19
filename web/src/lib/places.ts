import { useEffect, useState } from "react"
import { useRouterState } from "@tanstack/react-router"
import { screenTabs, type ScreenTab } from "@/lib/computer"

/**
 * The places worked in last, kept per environment in this browser, for Home's Continue
 * section: a teammate's thread or home, a computer's page, a review, a merge, a build or a
 * memory conflict, each as the address it was left at, tab and all.
 */
export type Place =
  | { kind: "teammate"; id: string; thread?: string; tab?: ScreenTab }
  | { kind: "sandbox"; id: string; tab?: ScreenTab }
  | { kind: "review" | "merge" | "build" | "conflict"; id: string }

export type Visit = {
  place: Place
  /** The address as it was left: path, search and hash. */
  href: string
  /** When it was left, in ms since the epoch. */
  at: number
}

/** How many places are kept; Home shows fewer. */
const kept = 8

/** Places older than this aren't offered. */
export const placeAge = 14 * 24 * 60 * 60 * 1000

const storeKey = (env: string) => `studio.places.${env}`

const tabOf = (s: Record<string, unknown>) =>
  screenTabs.find((t) => t === s.tab)

/**
 * The place an address of the environment shows, or none for a list, Home, settings and the
 * pages that only redirect.
 */
export function placeOf(
  env: string,
  pathname: string,
  search: Record<string, unknown>
): Place | undefined {
  const prefix = `/e/${env}/`
  if (!pathname.startsWith(prefix)) return undefined
  const parts = pathname.slice(prefix.length).split("/").filter(Boolean)
  const id = parts[1] ? decodeURIComponent(parts[1]) : undefined
  if (!id) return undefined
  const one = parts.length === 2
  const text = (v: unknown) => (typeof v === "string" && v ? v : undefined)
  switch (parts[0]) {
    case "teammates": {
      // Its settings are not where work goes on.
      if (!one || search.profile) return undefined
      const thread = text(search.thread)
      const tab = tabOf(search)
      return {
        kind: "teammate",
        id,
        ...(thread ? { thread } : {}),
        ...(tab ? { tab } : {}),
      }
    }
    case "sandboxes": {
      if (!one) return undefined
      const tab = tabOf(search)
      return { kind: "sandbox", id, ...(tab ? { tab } : {}) }
    }
    case "merges":
      return one ? { kind: "merge", id } : undefined
    case "builds":
      return one ? { kind: "build", id } : undefined
    case "git":
      return parts[1] === "reviews" && parts[2] && parts.length === 3
        ? { kind: "review", id: decodeURIComponent(parts[2]) }
        : undefined
    case "memory":
      return parts[1] === "conflicts" && parts[2] && parts.length === 3
        ? { kind: "conflict", id: decodeURIComponent(parts[2]) }
        : undefined
  }
  return undefined
}

/** One entry per thread, computer or item: moving between its tabs updates it. */
export function sameEntry(a: Place, b: Place) {
  if (a.kind !== b.kind || a.id !== b.id) return false
  return a.kind !== "teammate" || a.thread === (b as typeof a).thread
}

export function loadVisits(env: string): Visit[] {
  try {
    const v: unknown = JSON.parse(localStorage.getItem(storeKey(env)) ?? "[]")
    return Array.isArray(v)
      ? v.filter(
          (x): x is Visit =>
            typeof x?.href === "string" &&
            typeof x?.at === "number" &&
            typeof x?.place?.kind === "string" &&
            typeof x?.place?.id === "string"
        )
      : []
  } catch {
    return []
  }
}

/** Records a place as the last one worked in, newest first. */
export function recordVisit(env: string, visit: Visit) {
  const rest = loadVisits(env).filter((v) => !sameEntry(v.place, visit.place))
  try {
    localStorage.setItem(
      storeKey(env),
      JSON.stringify([visit, ...rest].slice(0, kept))
    )
  } catch {
    // Without storage Home just has nothing to continue.
  }
}

/** Forgets a place, as one that is gone. */
export function forgetVisit(env: string, place: Place) {
  const rest = loadVisits(env).filter((v) => !sameEntry(v.place, place))
  try {
    localStorage.setItem(storeKey(env), JSON.stringify(rest))
  } catch {
    // It is left out of the list anyway.
  }
}

/**
 * Records each place of the environment the router settles on, so an address that only
 * redirects is never one. It is kept as it was last seen, so a thread comes back on the tab
 * it was left on, and with when it was left: on the way to another address, or when the page
 * is hidden or closed.
 */
export function useRecordPlaces(env: string) {
  const location = useRouterState({ select: (s) => s.resolvedLocation })
  const href = location?.href
  const found = location && placeOf(env, location.pathname, location.search)
  // As text, so the effect runs again only when the place or its address changes.
  const place = found ? JSON.stringify(found) : undefined
  useEffect(() => {
    if (!href || !place) return
    const visit = { place: JSON.parse(place) as Place, href }
    const record = () => recordVisit(env, { ...visit, at: Date.now() })
    const hidden = () => {
      if (document.visibilityState === "hidden") record()
    }
    record()
    document.addEventListener("visibilitychange", hidden)
    window.addEventListener("pagehide", record)
    return () => {
      document.removeEventListener("visibilitychange", hidden)
      window.removeEventListener("pagehide", record)
      record()
    }
  }, [env, href, place])
}

/** The environment's places as they were when the page opened. */
export function useVisits(env: string) {
  const [visits] = useState(() => loadVisits(env))
  return visits
}
