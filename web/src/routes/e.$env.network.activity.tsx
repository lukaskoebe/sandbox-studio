import { createFileRoute } from "@tanstack/react-router"
import { ClockCounterClockwiseIcon } from "@phosphor-icons/react"
import { Ident } from "@/components/ident"
import { SectionHeader } from "@/components/section-header"
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
  return (
    <div className="space-y-2">
      <SectionHeader
        title="Decisions"
        icon={ClockCounterClockwiseIcon}
        area="network"
        count={decided.length}
      />
      {!decided.length ? (
        <p className="text-xs text-muted-foreground">No decisions yet.</p>
      ) : (
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
                  <TableCell>
                    <Ident>{a.subject}</Ident>
                  </TableCell>
                  <TableCell>{a.network?.sandboxName}</TableCell>
                  <TableCell className="capitalize">{a.status}</TableCell>
                  <TableCell>{a.attempts}</TableCell>
                  <TableCell className="text-muted-foreground">
                    {formatAge(a.updatedAt)}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
    </div>
  )
}
