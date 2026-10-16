import { useSyncExternalStore } from "react"
import type { Chat } from "@/lib/chats"

/**
 * Which chats this browser has seen, and up to which change: a chat whose rev passed the one
 * seen, and isn't working any more, has news. Chats first met after `since` are news until
 * opened; the ones from before the first visit aren't. It is a per-viewer convenience kept
 * in localStorage, so another browser keeps its own.
 */
type Seen = { since: number; revs: Partial<Record<string, number>> }

const key = "studio.chats.seen"
/** Most chats remembered; the ones seen longest ago are forgotten first. */
const maxChats = 500

let cache: Seen | undefined
const listeners = new Set<() => void>()

function load(): Seen {
  if (cache) return cache
  try {
    const parsed = JSON.parse(localStorage.getItem(key) ?? "null") as Partial<
      Record<keyof Seen, unknown>
    > | null
    if (
      parsed &&
      typeof parsed.since === "number" &&
      typeof parsed.revs === "object" &&
      parsed.revs !== null
    ) {
      cache = { since: parsed.since, revs: parsed.revs }
      return cache
    }
  } catch {
    // Unreadable or blocked storage: start afresh, in memory if need be.
  }
  cache = { since: Date.now(), revs: {} }
  save()
  return cache
}

function save() {
  try {
    localStorage.setItem(key, JSON.stringify(cache))
  } catch {
    // Kept in memory for this page.
  }
}

function subscribe(listener: () => void) {
  listeners.add(listener)
  const onStorage = (e: StorageEvent) => {
    if (e.key !== key) return
    cache = undefined
    listener()
  }
  window.addEventListener("storage", onStorage)
  return () => {
    listeners.delete(listener)
    window.removeEventListener("storage", onStorage)
  }
}

/** Records that the chat was seen as of rev. */
export function markChatSeen(id: string, rev: number) {
  const seen = load()
  if ((seen.revs[id] ?? -1) >= rev) return
  const { [id]: _, ...rest } = seen.revs
  const entries = Object.entries(rest)
  const kept = entries.slice(Math.max(0, entries.length - maxChats + 1))
  cache = {
    since: seen.since,
    revs: { ...Object.fromEntries(kept), [id]: rev },
  }
  save()
  for (const l of listeners) l()
}

/** Whether a chat has changes this browser hasn't seen, now that its agent is done. */
export function hasNews(c: Chat, seen: Seen) {
  if (c.state === "running") return false
  const rev = seen.revs[c.id]
  // Chats are timed to the second, so one from the second of the first visit is new too.
  if (rev === undefined)
    return Date.parse(c.createdAt) >= Math.floor(seen.since / 1000) * 1000
  return c.rev > rev
}

/** This browser's seen chats, current across components and tabs. */
export function useChatsSeen() {
  return useSyncExternalStore(subscribe, load, load)
}
