import { useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { ScrollArea } from "@/components/ui/scroll-area"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/spinner"
import {
  decideEach,
  decisionBody,
  decisionLabel,
  ruleSummary,
  defaultReach,
  isBrowserRequest,
  LifetimeToggle,
  ReachToggle,
  refreshDecisions,
  ScopeToggle,
  type BrowserReach,
  type DecisionScope,
  type Lifetime,
} from "@/components/approval-decision"
import { HoldStatus } from "@/components/hold-status"
import { Ident } from "@/components/ident"
import type { Approval } from "@/lib/api/client"
import { approvalSource } from "@/lib/approval-kinds"
import { heldFor, isConnection, useHoldClock } from "@/lib/connection-holds"
import { cn, hostPort } from "@/lib/utils"

/**
 * The network requests one sandbox made close together, as a Needs you row shows them opened
 * (components/needs-you.tsx): the hosts, then Allow all, Deny all, or Review individually for a
 * row per host. Allow all and Deny all decide each host as its own row would, for who and how
 * long Options say: with a rule of its own, for the browser session, or once.
 */
export function NetworkGroupBody({
  approvals,
  envName,
  onReview,
  className,
}: {
  approvals: Approval[]
  envName?: string
  onReview: () => void
  className?: string
}) {
  const queryClient = useQueryClient()
  const first = approvals[0]
  // As on a single card: a teammate's sandbox starts with the scope its teammate sets.
  const owner = first.teammate
  const [scope, setScope] = useState<DecisionScope>(
    owner?.approvalScope ?? "environment"
  )
  const browser = isBrowserRequest(first)
  const [reach, setReach] = useState<BrowserReach>(defaultReach(first))
  const [picked, setLifetime] = useState<Lifetime>("always")
  // Connections nothing holds open any more can only be answered for their next attempts.
  const connections = isConnection(first)
  const now = useHoldClock(connections ? approvals : [])
  const gaveUp =
    connections && approvals.every((a) => heldFor(a, now) === undefined)
  const lifetime = gaveUp && picked === "once" ? "always" : picked
  const [optionsOpen, setOptionsOpen] = useState(false)
  const [working, setWorking] = useState<"allow" | "deny">()
  const hosts = approvals.map((a) =>
    a.network ? hostPort(a.network.host, a.network.port) : a.subject
  )

  async function decideAll(action: "allow" | "deny") {
    setWorking(action)
    const failure = await decideEach(approvals, (a) =>
      decisionBody(a, action, {
        lifetime,
        scope,
        reach,
        host: a.network?.host,
      })
    )
    if (failure) {
      toast.error(`Could not ${action} every host`, { description: failure })
    }
    await refreshDecisions(queryClient)
    setWorking(undefined)
  }

  const body = (
    <>
      {connections && <HoldStatus approvals={approvals} now={now} />}
      <ScrollArea viewportClassName="max-h-28">
        <ul className="flex flex-wrap gap-1 text-xs">
          {hosts.map((host, i) => (
            <li key={`${host}-${i}`} className="max-w-full">
              <Ident>{host}</Ident>
            </li>
          ))}
        </ul>
      </ScrollArea>
      {!optionsOpen && (
        <p className="text-caption text-muted-foreground">
          {ruleSummary({
            lifetime,
            scope,
            reach,
            browser,
            sandboxName: first.network?.sandboxName,
            owner: owner?.name,
            envName,
            many: true,
          })}
        </p>
      )}
      {optionsOpen && (
        <>
          {browser ? (
            <ReachToggle
              value={reach}
              onChange={setReach}
              owner={owner?.name}
              disabled={lifetime === "session" || lifetime === "once"}
            />
          ) : (
            <ScopeToggle
              value={scope}
              onChange={setScope}
              sandboxName={approvalSource(first)}
              owner={owner?.name}
              envName={envName}
            />
          )}
          <LifetimeToggle
            value={lifetime}
            onChange={setLifetime}
            browser={browser}
            once={!gaveUp}
          />
        </>
      )}
      <div className="flex flex-wrap items-center gap-1.5">
        <Button variant="ghost" size="xs" onClick={onReview}>
          Review individually
        </Button>
        <Button
          variant="ghost"
          size="xs"
          aria-expanded={optionsOpen}
          onClick={() => setOptionsOpen(!optionsOpen)}
        >
          Options
        </Button>
        <Button
          variant="outline"
          size="sm"
          className="ml-auto"
          disabled={working !== undefined}
          onClick={() => void decideAll("deny")}
        >
          {working === "deny" && <Spinner />}
          {decisionLabel("deny", lifetime, true)}
        </Button>
        <Button
          size="sm"
          disabled={working !== undefined}
          onClick={() => void decideAll("allow")}
        >
          {working === "allow" && <Spinner />}
          {decisionLabel("allow", lifetime, true)}
        </Button>
      </div>
    </>
  )
  return <div className={cn("grid gap-2", className)}>{body}</div>
}
