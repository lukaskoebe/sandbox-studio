import { useEffect, useSyncExternalStore, type RefObject } from "react"

/**
 * A new thread with a teammate, asked for from elsewhere (the sidebar's +, ⌘K): its home opens
 * and the composer there, which starts threads, takes the focus. Each ask is its own, so the
 * same teammate twice focuses twice.
 */
let asked: { teammate: string } | undefined
const listeners = new Set<() => void>()

export function newThreadWith(teammate: string) {
  asked = { teammate }
  for (const l of listeners) l()
}

/** Focuses the text box in box when a new thread with teammate was asked for. */
export function useNewThreadFocus(
  teammate: string,
  box: RefObject<HTMLElement | null>
) {
  const ask = useSyncExternalStore(
    (onChange) => {
      listeners.add(onChange)
      return () => listeners.delete(onChange)
    },
    () => asked
  )
  useEffect(() => {
    if (ask?.teammate !== teammate) return
    const area = box.current?.querySelector("textarea")
    if (!area) return
    asked = undefined
    area.focus()
  }, [ask, teammate, box])
}
