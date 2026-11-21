import { useEffect, useState, useSyncExternalStore } from "react"
import type { Approval } from "@/lib/api/client"

/**
 * When the pending approvals that came after the page loaded arrived. A new one flashes the
 * top bar's count, and its card slides into Home's list. Those pending at the first load are
 * never arrivals, so opening Studio doesn't animate what already waited.
 */
let known: Set<string> | null = null
const arrived = new Map<string, number>()
let arrivals = 0
const listeners = new Set<() => void>()

/** How long after its arrival a card that mounts still shows arriving, in ms. */
const fresh = 5000

/** Records each load of the pending approvals; the root mounts it once. */
export function useRecordArrivals(pending: Approval[] | null | undefined) {
  useEffect(() => {
    if (!pending) return
    if (known === null) {
      known = new Set(pending.map((a) => a.id))
      return
    }
    const now = Date.now()
    let any = false
    for (const a of pending) {
      if (known.has(a.id)) continue
      known.add(a.id)
      arrived.set(a.id, now)
      any = true
    }
    for (const [id, at] of arrived) if (now - at > fresh) arrived.delete(id)
    if (any) {
      arrivals++
      listeners.forEach((l) => l())
    }
  }, [pending])
}

const subscribe = (l: () => void) => {
  listeners.add(l)
  return () => void listeners.delete(l)
}

/**
 * How many times approvals arrived since the calling component mounted: a key that changes
 * with each arrival, and is 0 until one comes.
 */
export function useArrivals() {
  const count = useSyncExternalStore(subscribe, () => arrivals)
  const [base] = useState(count)
  return count - base
}

/**
 * Whether an approval arrived a moment ago, so that its card shows arriving as it mounts. A card
 * can mount with the load that brought its approval, before the root records it: one the first
 * load didn't have and nothing recorded yet is arriving too.
 */
export function useJustArrived(id: string) {
  const [arriving] = useState(() => {
    if (known === null) return false
    if (!known.has(id)) return true
    const at = arrived.get(id)
    return at !== undefined && Date.now() - at < fresh
  })
  return arriving
}
