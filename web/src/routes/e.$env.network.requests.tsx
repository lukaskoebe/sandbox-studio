import { createFileRoute } from "@tanstack/react-router"
import { ApprovalDecision } from "@/components/approval-decision"
import { Spinner } from "@/components/ui/spinner"
import { $api } from "@/lib/api/client"
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
  const list = (requests.data ?? []).filter((a) => a.kind === "network")

  if (requests.isPending) return <Spinner className="mx-auto block" />
  if (!list.length)
    return (
      <p className="text-xs text-muted-foreground">
        Nothing is waiting for a decision.
      </p>
    )
  return (
    <ul className="divide-y rounded-lg ring-1 ring-foreground/10">
      {list.map((a) => (
        <li key={a.id} className="grid gap-2 p-3">
          <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-xs">
            <span className="font-mono">{a.subject}</span>
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
  )
}
