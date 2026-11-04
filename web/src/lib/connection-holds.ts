import { useEffect, useState } from "react"
import type { Approval } from "@/lib/api/client"

/** How long the gateway holds a connection for an answer (cmd/studio), for its progress track. */
export const holdSeconds = 60

/** Whether an approval is a sandbox's connection, as opposed to a browser's request. */
export function isConnection(a: Approval) {
  return a.kind === "network"
}

/** A sandbox's connections that wait for an answer. */
export function waitingConnections(
  pending: Approval[] | null | undefined,
  sandboxId: string
) {
  return (pending ?? []).filter(
    (a) => isConnection(a) && a.sandboxId === sandboxId
  )
}

/**
 * The seconds left until a connection held for the approval gives up, or undefined when nothing
 * holds it open any more: the client saw a failure, and an answer only helps its next attempt.
 */
export function heldFor(a: Approval, now: number): number | undefined {
  if (!a.heldUntil) return undefined
  const left = Math.ceil((Date.parse(a.heldUntil) - now) / 1000)
  return left > 0 ? left : undefined
}

/** The time to count holds down by, which moves each second while one of these is held. */
export function useHoldClock(approvals: Approval[]) {
  const [now, setNow] = useState(() => Date.now())
  const until = approvals.reduce(
    (last, a) => (a.heldUntil ? Math.max(last, Date.parse(a.heldUntil)) : last),
    0
  )
  useEffect(() => {
    setNow(Date.now())
    if (until <= Date.now()) return
    const timer = setInterval(() => {
      const t = Date.now()
      setNow(t)
      if (t > until) clearInterval(timer)
    }, 1000)
    return () => clearInterval(timer)
  }, [until])
  return now
}
