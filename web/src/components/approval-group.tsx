import { useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { ScrollArea } from "@/components/ui/scroll-area"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/spinner"
import {
  decisionBody,
  decisionLabel,
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
import { Ident } from "@/components/ident"
import { KindChip } from "@/components/kind-chip"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { errorMessage, fetchClient, type Approval } from "@/lib/api/client"
import { ruleClass } from "@/lib/accents"
import { groupSite } from "@/lib/approval-groups"
import { approvalKind, approvalSource } from "@/lib/approval-kinds"
import { cn, formatAge, hostPort } from "@/lib/utils"

/**
 * The network requests one sandbox made close together, as one card: the hosts, then Allow all,
 * Deny all, or Review individually for the per-host cards. Allow all and Deny all decide each
 * host as a single card would, for who and how long Options say: with a rule of its own, for
 * the browser session, or once. A browser page's requests say which site the page is on, and
 * the card is titled with it.
 */
export function NetworkGroupCard({
  approvals,
  envName,
  onReview,
}: {
  approvals: Approval[]
  envName?: string
  onReview: () => void
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
  const [lifetime, setLifetime] = useState<Lifetime>("always")
  const [optionsOpen, setOptionsOpen] = useState(false)
  const [working, setWorking] = useState<"allow" | "deny">()
  const kind = approvalKind(first)
  const sandbox = approvalSource(first)
  const site = groupSite(approvals)
  const details = [site && sandbox, envName, formatAge(first.createdAt)]
    .filter(Boolean)
    .join(" · ")
  const hosts = approvals.map((a) =>
    a.network ? hostPort(a.network.host, a.network.port) : a.subject
  )

  // Decides the hosts in turn. A host that an earlier rule of this run already covers answers
  // 409 and is skipped.
  async function decideAll(action: "allow" | "deny") {
    setWorking(action)
    let failure: string | undefined
    for (const a of approvals) {
      const { error, response } = await fetchClient.POST(
        "/api/environments/{env}/approvals/{id}/decide",
        {
          params: { path: { env: a.environmentId, id: a.id } },
          body: decisionBody(a, action, {
            lifetime,
            scope,
            reach,
            host: a.network?.host,
          }),
        }
      )
      if (error && response.status !== 409) failure ??= errorMessage(error)
    }
    setWorking(undefined)
    refreshDecisions(queryClient)
    if (failure) {
      toast.error(`Could not ${action} every host`, { description: failure })
    }
  }

  return (
    <Card
      size="sm"
      className={cn("gap-2 border-l-[3px]", ruleClass(kind.area))}
      data-testid="approval-group"
    >
      <CardHeader>
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
          <KindChip area={kind.area} icon={kind.icon}>
            {kind.title}
          </KindChip>
          <CardDescription className="min-w-0 break-words">
            {details}
          </CardDescription>
        </div>
        <CardTitle className="min-w-0 break-words">
          {`${site ?? sandbox ?? "A sandbox"} wants ${approvals.length} hosts`}
        </CardTitle>
      </CardHeader>
      <CardContent className="grid gap-2">
        <ScrollArea viewportClassName="max-h-28">
          <ul className="flex flex-wrap gap-1 text-xs">
            {hosts.map((host, i) => (
              <li key={`${host}-${i}`} className="max-w-full">
                <Ident>{host}</Ident>
              </li>
            ))}
          </ul>
        </ScrollArea>
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
                sandboxName={sandbox}
                owner={owner?.name}
              />
            )}
            <LifetimeToggle
              value={lifetime}
              onChange={setLifetime}
              browser={browser}
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
      </CardContent>
    </Card>
  )
}
