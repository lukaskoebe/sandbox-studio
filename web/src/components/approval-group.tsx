import { useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/spinner"
import {
  refreshDecisions,
  ScopeToggle,
  type DecisionScope,
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
import { approvalKind, approvalSource } from "@/lib/approval-kinds"
import { cn, formatAge, hostPort } from "@/lib/utils"

/**
 * The network requests one sandbox made close together, as one card: the hosts, then Allow all,
 * Deny all, or Review individually for the per-host cards. Allow all and Deny all decide each
 * host with a rule of its own, covering the scope chosen under Options as on a single card.
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
  const [scope, setScope] = useState<DecisionScope>("environment")
  const [optionsOpen, setOptionsOpen] = useState(false)
  const [working, setWorking] = useState<"allow" | "deny">()
  const first = approvals[0]
  const kind = approvalKind(first)
  const sandbox = approvalSource(first)
  const details = [envName, formatAge(first.createdAt)]
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
          body: { action, scope, host: a.network?.host },
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
      className={cn("gap-2 border-l-[3px] shadow-lg", ruleClass(kind.area))}
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
          {`${sandbox ?? "A sandbox"} wants ${approvals.length} hosts`}
        </CardTitle>
      </CardHeader>
      <CardContent className="grid gap-2">
        <ul className="flex max-h-28 flex-wrap gap-1 overflow-y-auto text-xs">
          {hosts.map((host, i) => (
            <li key={`${host}-${i}`} className="max-w-full">
              <Ident>{host}</Ident>
            </li>
          ))}
        </ul>
        {optionsOpen && (
          <ScopeToggle
            value={scope}
            onChange={setScope}
            sandboxName={sandbox}
          />
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
            Deny all
          </Button>
          <Button
            size="sm"
            disabled={working !== undefined}
            onClick={() => void decideAll("allow")}
          >
            {working === "allow" && <Spinner />}
            Allow all
          </Button>
        </div>
      </CardContent>
    </Card>
  )
}
