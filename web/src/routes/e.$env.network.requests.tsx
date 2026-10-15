import { createFileRoute } from "@tanstack/react-router"
import { HourglassIcon } from "@phosphor-icons/react"
import { ApprovalDecision } from "@/components/approval-decision"
import { Ident } from "@/components/ident"
import { SectionHeader } from "@/components/section-header"
import { Spinner } from "@/components/ui/spinner"
import { $api } from "@/lib/api/client"
import { useClaimApprovals } from "@/lib/approval-claims"
import { isNetworkApproval } from "@/lib/approval-kinds"
import { formatAge } from "@/lib/utils"

export const Route = createFileRoute("/e/$env/network/requests")({
  component: RequestsTab,
})

/** Connections waiting for a decision. Allow or Deny creates a rule; Ignore only closes the request. */
function RequestsTab() {
  const { env } = Route.useParams()
  const requests = $api.useQuery("get", "/api/environments/{env}/approvals", {
    params: { path: { env }, query: { status: "pending" } },
  })
  // Git approvals are reviewed on the Git page.
  const list = (requests.data ?? []).filter(isNetworkApproval)
  useClaimApprovals(list.map((a) => a.id))

  if (requests.isPending) return <Spinner className="mx-auto block" />
  return (
    <div className="space-y-2">
      <SectionHeader
        title="Waiting for you"
        icon={HourglassIcon}
        area="network"
        count={list.length}
      />
      {!list.length ? (
        <p className="text-xs text-muted-foreground">
          Nothing is waiting for a decision.
        </p>
      ) : (
        <ul className="divide-y rounded-lg ring-1 ring-foreground/10">
          {list.map((a) => (
            <li key={a.id} className="grid gap-2 p-3">
              <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-xs">
                <Ident>{a.subject}</Ident>
                <span className="text-muted-foreground">
                  {a.network?.sandboxName}
                </span>
                <span className="ml-auto text-muted-foreground">
                  {a.attempts} {a.attempts === 1 ? "attempt" : "attempts"} ·{" "}
                  {formatAge(a.createdAt)}
                </span>
              </div>
              <ApprovalDecision approval={a} />
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
