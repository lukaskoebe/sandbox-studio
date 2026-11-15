import { useSyncExternalStore } from "react"

/** What Start work starts: a chat, the teammate's agent in a terminal, or only its computer. */
export type StartWorkMode = "chat" | "terminal" | "computer"

/**
 * What the Start work dialog (components/teammate/start-work.tsx) opens with: the teammate, the
 * computer it suggests, the way to start, and for Bring in the terminal whose lines and the
 * changes come along.
 */
export type StartWorkRequest = {
  teammate: string
  computer?: string
  mode?: StartWorkMode
  /** The terminal the user worked in, on computer: Bring in offers its lines and the changes. */
  bring?: { term: string }
}

// The sidebar's +, the teammate's home and its desk open the dialog from anywhere.
let current: StartWorkRequest | null = null
const listeners = new Set<() => void>()

export function openStartWork(req: StartWorkRequest | null) {
  current = req
  for (const l of listeners) l()
}

export function useStartWork() {
  return useSyncExternalStore(
    (onChange) => {
      listeners.add(onChange)
      return () => listeners.delete(onChange)
    },
    () => current
  )
}
