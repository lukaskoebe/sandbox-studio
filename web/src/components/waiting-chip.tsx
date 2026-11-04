import { useState } from "react"
import { ConnectionsSheet } from "@/components/connections-panel"
import { Ident } from "@/components/ident"
import { StatusDot } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover"
import { Spinner } from "@/components/ui/spinner"
import {
  approvalHost,
  useAnswerConnections,
} from "@/components/waiting-requests"
import { usePendingApprovals } from "@/hooks/use-pending-approvals"
import type { Sandbox } from "@/lib/api/client"
import { useClaimApprovals } from "@/lib/approval-claims"
import {
  heldFor,
  holdSeconds,
  useHoldClock,
  waitingConnections,
} from "@/lib/connection-holds"
import { useNotificationPermission } from "@/lib/notifications"

/**
 * How many of a sandbox's connections wait for an answer, on its screen. Nothing opens by itself:
 * the count opens the hosts, to answer them together or to review them in Connections. While it
 * shows, the floating stack leaves them out.
 */
export function WaitingChip({
  env,
  sandbox,
}: {
  env: string
  sandbox: Sandbox
}) {
  const pending = usePendingApprovals()
  const waiting = waitingConnections(pending.data, sandbox.id)
  useClaimApprovals(waiting.map((a) => a.id))
  const now = useHoldClock(waiting)
  const [open, setOpen] = useState(false)
  const [reviewing, setReviewing] = useState(false)
  const { working, answer } = useAnswerConnections()
  const { permission, request } = useNotificationPermission()
  const anyHeld = waiting.some((a) => heldFor(a, now) !== undefined)
  const words = anyHeld
    ? `${waiting.length} ${waiting.length === 1 ? "request" : "requests"} waiting`
    : `${waiting.length} gave up waiting`
  const choice = {
    scope: waiting.at(0)?.teammate?.approvalScope ?? "environment",
    lifetime: "always",
  } as const
  return (
    <>
      {waiting.length > 0 && (
        <Popover open={open} onOpenChange={setOpen}>
          <PopoverTrigger
            render={
              <Button
                variant="outline"
                size="xs"
                aria-label={words}
                className={
                  anyHeld ? "shrink-0 border-(--st-waiting)" : "shrink-0"
                }
              />
            }
          >
            <StatusDot
              status={anyHeld ? "waiting" : "stopped"}
              compact
              label={words}
            />
            {/* A narrow screen keeps only the count. */}
            <span className="@max-[44rem]:hidden">{words}</span>
            <span className="tabular-nums @min-[44rem]:hidden">
              {waiting.length}
            </span>
          </PopoverTrigger>
          <PopoverContent
            data-testid="waiting-popover"
            className="grid w-[26rem] gap-2 text-xs"
          >
            <div className="grid gap-0.5">
              <strong className="text-sm font-medium">
                {sandbox.name} wants to connect
              </strong>
              <p className="text-muted-foreground">
                {anyHeld
                  ? `Held for up to ${holdSeconds} s, then the client sees a failure.`
                  : "Nothing holds these open any more. Allow them, then have the client try again."}
              </p>
            </div>
            <ul className="grid gap-1">
              {waiting.map((a) => {
                const left = heldFor(a, now)
                return (
                  <li key={a.id} className="flex min-w-0 items-center gap-2">
                    <StatusDot
                      status={left === undefined ? "stopped" : "waiting"}
                      compact
                      label={left === undefined ? "no longer held" : "held"}
                      className="shrink-0"
                    />
                    <Ident className="min-w-0 flex-1">{approvalHost(a)}</Ident>
                    <span className="shrink-0 font-mono text-caption text-muted-foreground tabular-nums">
                      {left === undefined ? "gave up" : `${left} s left`}
                    </span>
                  </li>
                )
              })}
            </ul>
            <div className="flex flex-wrap items-center gap-1.5 pt-1">
              <Button
                size="xs"
                disabled={working !== undefined}
                onClick={() => void answer("allow", waiting, "allow", choice)}
              >
                {working === "allow" && <Spinner className="size-3" />}
                Allow all
              </Button>
              <Button
                variant="outline"
                size="xs"
                disabled={working !== undefined}
                onClick={() => void answer("deny", waiting, "deny", choice)}
              >
                {working === "deny" && <Spinner className="size-3" />}
                Deny all
              </Button>
              <Button
                variant="ghost"
                size="xs"
                className="ml-auto"
                onClick={() => {
                  setOpen(false)
                  setReviewing(true)
                }}
              >
                Review individually
              </Button>
            </div>
            {permission === "default" && (
              <Button
                variant="link"
                size="xs"
                className="justify-self-start px-0"
                onClick={request}
              >
                Enable notifications
              </Button>
            )}
          </PopoverContent>
        </Popover>
      )}
      <ConnectionsSheet
        env={env}
        sandbox={sandbox}
        open={reviewing}
        onOpenChange={setReviewing}
      />
    </>
  )
}
