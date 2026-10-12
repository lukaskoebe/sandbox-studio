import { createFileRoute } from "@tanstack/react-router"
import { ApprovalStatusBadge } from "@/components/status-badge"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { Spinner } from "@/components/ui/spinner"
import { $api } from "@/lib/api/client"
import { formatAge } from "@/lib/utils"

export const Route = createFileRoute("/e/$env/network/activity")({
  component: ActivityTab,
})

/** Network requests already decided, newest first: allowed, denied or ignored. */
function ActivityTab() {
  const { env } = Route.useParams()
  const approvals = $api.useQuery("get", "/api/environments/{env}/approvals", {
    params: { path: { env }, query: { status: "all" } },
  })
  const decided = (approvals.data ?? [])
    .filter((a) => a.kind === "network" && a.status !== "pending")
    .slice(0, 50)

  if (approvals.isPending) return <Spinner className="mx-auto block" />
  if (!decided.length)
    return <p className="text-xs text-muted-foreground">No decisions yet.</p>
  return (
    <div className="rounded-lg ring-1 ring-foreground/10">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Host</TableHead>
            <TableHead>Sandbox</TableHead>
            <TableHead>Decision</TableHead>
            <TableHead>Attempts</TableHead>
            <TableHead>Decided</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {decided.map((a) => (
            <TableRow key={a.id}>
              <TableCell className="font-mono">{a.subject}</TableCell>
              <TableCell>{a.network?.sandboxName}</TableCell>
              <TableCell>
                <ApprovalStatusBadge status={a.status} />
              </TableCell>
              <TableCell>{a.attempts}</TableCell>
              <TableCell className="text-muted-foreground">
                {formatAge(a.updatedAt)}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  )
}
