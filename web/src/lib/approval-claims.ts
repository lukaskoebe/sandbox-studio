import { useEffect, useSyncExternalStore } from "react"

/**
 * Which pending approvals the page shows in place: a chat's permission cards, Home's and a
 * teammate's cards, the network requests list, the browser's questions on its page. The
 * floating stack leaves claimed approvals out, so that one approval shows in one place.
 */
const claims = new Map<string, number>()
const listeners = new Set<() => void>()
let claimed: ReadonlySet<string> = new Set()

function changed() {
  claimed = new Set(claims.keys())
  listeners.forEach((l) => l())
}

/** Claims the approvals with these IDs while the calling component is mounted. */
export function useClaimApprovals(ids: readonly (string | undefined)[]) {
  const key = ids.filter(Boolean).sort().join("\n")
  useEffect(() => {
    if (!key) return
    const list = key.split("\n")
    for (const id of list) claims.set(id, (claims.get(id) ?? 0) + 1)
    changed()
    return () => {
      for (const id of list) {
        const n = (claims.get(id) ?? 1) - 1
        if (n > 0) claims.set(id, n)
        else claims.delete(id)
      }
      changed()
    }
  }, [key])
}

/** The IDs of the approvals the page shows in place. */
export function useClaimedApprovals(): ReadonlySet<string> {
  return useSyncExternalStore(
    (l) => {
      listeners.add(l)
      return () => listeners.delete(l)
    },
    () => claimed
  )
}
