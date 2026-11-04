import { useState, type ReactNode } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  decideEach,
  decisionBody,
  decisionLabel,
  LifetimeToggle,
  refreshDecisions,
  ScopeToggle,
  type DecisionScope,
  type Lifetime,
} from "@/components/approval-decision"
import { Ident } from "@/components/ident"
import { StatusDot } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/spinner"
import type { Approval, Sandbox } from "@/lib/api/client"
import { heldFor, useHoldClock } from "@/lib/connection-holds"
import { hostPort } from "@/lib/utils"

/** What an approval asks to reach. */
export function approvalHost(a: Approval) {
  return a.network ? hostPort(a.network.host, a.network.port) : a.subject
}

type Action = "allow" | "deny" | "dismiss"

/**
 * Answers a sandbox's waiting connections as a rule per host, for the scope and time given: the
 * way a single card's Allow and Deny do, for the host it asked about. Says what failed.
 */
export function useAnswerConnections() {
  const queryClient = useQueryClient()
  const [working, setWorking] = useState<string>()
  async function answer(
    key: string,
    approvals: Approval[],
    action: Action,
    choice: { scope: DecisionScope; lifetime: Lifetime }
  ) {
    setWorking(key)
    const failure = await decideEach(approvals, (a) =>
      action === "dismiss"
        ? { action, scope: choice.scope }
        : decisionBody(a, action, {
            ...choice,
            reach: "browser",
            host: a.network?.host,
          })
    )
    setWorking(undefined)
    refreshDecisions(queryClient)
    if (failure) {
      toast.error(`Could not ${action} every host`, { description: failure })
    }
  }
  return { working, answer }
}

/** A host and what to do about it, as a row of the Connections list. */
function WaitingRow({
  approval,
  status,
  detail,
  children,
}: {
  approval: Approval
  status: "waiting" | "stopped"
  detail?: string
  children: ReactNode
}) {
  return (
    <li
      data-testid="waiting-request"
      className="flex flex-wrap items-center gap-x-2 gap-y-1 py-1 text-xs"
    >
      <StatusDot
        status={status}
        compact
        label={status === "waiting" ? "held" : "no longer held"}
        className="shrink-0"
      />
      <Ident className="min-w-0 flex-1 basis-40">
        {approvalHost(approval)}
      </Ident>
      {detail && (
        <span className="shrink-0 font-mono text-caption whitespace-nowrap text-muted-foreground tabular-nums">
          {detail}
        </span>
      )}
      <span className="ml-auto flex shrink-0 gap-1.5">{children}</span>
    </li>
  )
}

/** The words for a scope and a time, as the folded Options say them. */
function optionsSummary(
  scope: DecisionScope,
  lifetime: Lifetime,
  owner?: string
) {
  const who =
    scope === "environment"
      ? "this environment"
      : scope === "teammate"
        ? `${owner ?? "the teammate"}'s computers`
        : "this sandbox"
  const how =
    lifetime === "hour" ? "1 hour" : lifetime === "once" ? "once" : "always"
  return `Options: ${who}, ${how}`
}

/**
 * What of a sandbox's connections waits for an answer, on top of its Connections list. A
 * connection still held can be answered in time. One that nothing holds open any more has
 * failed for its client, so an answer is for its next attempt.
 */
export function WaitingRequests({
  approvals,
  sandbox,
}: {
  approvals: Approval[]
  sandbox: Sandbox
}) {
  const owner = approvals.at(0)?.teammate
  const [chosenScope, setScope] = useState<DecisionScope>()
  const scope = chosenScope ?? owner?.approvalScope ?? "environment"
  const [lifetime, setLifetime] = useState<Lifetime>("always")
  const [optionsOpen, setOptionsOpen] = useState(false)
  const { working, answer } = useAnswerConnections()
  const now = useHoldClock(approvals)
  if (approvals.length === 0) return null

  const held = approvals.filter((a) => heldFor(a, now) !== undefined)
  const gone = approvals.filter((a) => heldFor(a, now) === undefined)
  // "Once" answers only connections that are held, so it does nothing for the ones that aren't.
  const later: Lifetime = lifetime === "once" ? "always" : lifetime
  const busy = working !== undefined
  const button = (
    key: string,
    list: Approval[],
    action: Action,
    label: string,
    primary = false
  ) => (
    <Button
      variant={primary ? "default" : "outline"}
      size="xs"
      disabled={busy}
      onClick={() =>
        void answer(key, list, action, {
          scope,
          lifetime: gone.includes(list[0]) ? later : lifetime,
        })
      }
    >
      {working === key && <Spinner className="size-3" />}
      {label}
    </Button>
  )
  const options = (
    <div className="grid gap-2 pt-1">
      <Button
        variant="ghost"
        size="xs"
        className="justify-self-start px-0 text-muted-foreground"
        aria-expanded={optionsOpen}
        onClick={() => setOptionsOpen(!optionsOpen)}
      >
        {optionsSummary(scope, held.length > 0 ? lifetime : later, owner?.name)}
      </Button>
      {optionsOpen && (
        <>
          <ScopeToggle
            value={scope}
            onChange={setScope}
            sandboxName={sandbox.name}
            owner={owner?.name}
          />
          <LifetimeToggle
            value={held.length > 0 ? lifetime : later}
            onChange={setLifetime}
            browser={false}
            once={held.length > 0}
          />
        </>
      )}
    </div>
  )

  return (
    <div className="grid gap-3 px-4 pb-3">
      {held.length > 0 && (
        <section
          aria-label="Waiting for you"
          data-testid="waiting-held"
          className="rounded-md border bg-card px-3 py-2"
        >
          <div className="flex flex-wrap items-center justify-between gap-2 pb-1">
            <h3 className="section-label text-muted-foreground">
              Waiting for you · {held.length}
            </h3>
            {held.length > 1 && (
              <span className="flex gap-1.5">
                {button(
                  "deny-all",
                  held,
                  "deny",
                  decisionLabel("deny", lifetime, true)
                )}
                {button(
                  "allow-all",
                  held,
                  "allow",
                  decisionLabel("allow", lifetime, true),
                  true
                )}
              </span>
            )}
          </div>
          <ul>
            {held.map((a) => (
              <WaitingRow
                key={a.id}
                approval={a}
                status="waiting"
                detail={`${heldFor(a, now)} s left`}
              >
                {button(
                  `deny-${a.id}`,
                  [a],
                  "deny",
                  decisionLabel("deny", lifetime)
                )}
                {button(
                  `allow-${a.id}`,
                  [a],
                  "allow",
                  decisionLabel("allow", lifetime),
                  held.length === 1
                )}
              </WaitingRow>
            ))}
          </ul>
          {options}
        </section>
      )}
      {gone.length > 0 && (
        <section
          aria-label="Gave up waiting"
          data-testid="waiting-gone"
          className="rounded-md border px-3 py-2"
        >
          <div className="flex flex-wrap items-center justify-between gap-2 pb-1">
            <h3 className="section-label text-muted-foreground">
              Gave up waiting · {gone.length}
            </h3>
            {gone.length > 1 && (
              <span className="flex gap-1.5">
                {button("dismiss-all", gone, "dismiss", "Dismiss all")}
                {button(
                  "later-all",
                  gone,
                  "allow",
                  later === "always"
                    ? "Allow all next time"
                    : decisionLabel("allow", later, true)
                )}
              </span>
            )}
          </div>
          <ul>
            {gone.map((a) => (
              <WaitingRow key={a.id} approval={a} status="stopped">
                {button(`dismiss-${a.id}`, [a], "dismiss", "Dismiss")}
                {button(
                  `later-${a.id}`,
                  [a],
                  "allow",
                  later === "always"
                    ? "Allow next time"
                    : decisionLabel("allow", later)
                )}
              </WaitingRow>
            ))}
          </ul>
          <p className="pt-1 text-xs text-muted-foreground">
            Nothing holds these open any more. Allow one, then have the client
            try again.
          </p>
          {held.length === 0 && options}
        </section>
      )}
    </div>
  )
}
