import { useEffect, useSyncExternalStore } from "react"

/**
 * The open thread's options, which its crumb's menu in the top bar offers: the thread page
 * holds them (and their dialogs) and publishes them here, the crumb reads them.
 */
export type ThreadActions = {
  chatId: string
  rename: () => void
  copyLink: () => void
  toTerminal: () => void
  remove: () => void
  /** The terminal takes the chat only between turns. */
  handable: boolean
  handing: boolean
  running: boolean
}

let current: ThreadActions | null = null
const listeners = new Set<() => void>()

const subscribe = (l: () => void) => {
  listeners.add(l)
  return () => void listeners.delete(l)
}

const set = (a: ThreadActions | null) => {
  current = a
  listeners.forEach((l) => l())
}

/** Offers a thread's options in its crumb while the calling component is mounted. */
export function usePublishThreadActions(actions: ThreadActions) {
  useEffect(() => {
    set(actions)
    return () => {
      if (current === actions) set(null)
    }
  }, [actions])
}

/** The options of the thread with this ID, while its page offers them. */
export function useThreadActions(chatId?: string) {
  const a = useSyncExternalStore(subscribe, () => current)
  return a && a.chatId === chatId ? a : undefined
}
