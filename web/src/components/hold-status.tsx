import type { Approval } from "@/lib/api/client"
import { heldFor, holdSeconds } from "@/lib/connection-holds"

/**
 * Whether connections still wait on a card's answer: how long they are held, on a thin track that
 * runs down, or that the client has already failed and an answer is for its next attempt.
 */
export function HoldStatus({
  approvals,
  now,
}: {
  approvals: Approval[]
  now: number
}) {
  const left = Math.max(0, ...approvals.map((a) => heldFor(a, now) ?? 0))
  if (left === 0) {
    return (
      <p data-testid="hold-status" className="text-xs text-muted-foreground">
        No longer held. The client saw a failure. Allow it, then have the client
        try again.
      </p>
    )
  }
  return (
    <div data-testid="hold-status" className="grid gap-1">
      <p className="text-xs text-muted-foreground tabular-nums">
        Connection held, {left} s left
      </p>
      <div aria-hidden className="h-0.5 bg-muted">
        <div
          className="h-full bg-(--st-waiting)"
          style={{ width: `${Math.min(100, (left / holdSeconds) * 100)}%` }}
        />
      </div>
    </div>
  )
}
