import { createFileRoute } from "@tanstack/react-router"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { ClockCounterClockwiseIcon } from "@phosphor-icons/react"
import { Ident } from "@/components/ident"
import { SectionHeader } from "@/components/section-header"
import { Button } from "@/components/ui/button"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { Spinner } from "@/components/ui/spinner"
import { $api, errorMessage } from "@/lib/api/client"
import { approvalDecision, isNetworkApproval } from "@/lib/approval-kinds"
import { formatAge, hostPort } from "@/lib/utils"

export const Route = createFileRoute("/e/$env/network/activity")({
  component: ActivityTab,
})

/** Network requests already decided, newest first: allowed, denied or ignored. */
function ActivityTab() {
  const { env } = Route.useParams()
  const queryClient = useQueryClient()
  const approvals = $api.useQuery("get", "/api/environments/{env}/approvals", {
    params: { path: { env }, query: { status: "all" } },
  })
  const revert = $api.useMutation(
    "post",
    "/api/environments/{env}/approvals/{id}/revert",
    {
      onSuccess: () =>
        queryClient.invalidateQueries({
          queryKey: ["get", "/api/environments/{env}/approvals"],
        }),
      onError: (err) =>
        toast.error("Could not revert the decision", {
          description: errorMessage(err),
        }),
    }
  )
  const decided = (approvals.data ?? [])
    .filter((a) => isNetworkApproval(a) && a.status !== "pending")
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
                <TableHead>Why</TableHead>
                <TableHead>Attempts</TableHead>
                <TableHead>Decided</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {decided.map((a) => (
                <TableRow key={a.id}>
                  <TableCell>
                    <Ident>
                      {a.network
                        ? hostPort(a.network.host, a.network.port)
                        : a.subject}
                    </Ident>
                  </TableCell>
                  <TableCell>{a.network?.sandboxName}</TableCell>
                  <TableCell>
                    <span className="inline-flex flex-wrap items-center gap-2">
                      {approvalDecision(a)}
                      {a.network?.decided === "reviewer" && !a.reverted && (
                        <Button
                          variant="outline"
                          size="xs"
                          disabled={revert.isPending}
                          onClick={() =>
                            revert.mutate({
                              params: { path: { env, id: a.id } },
                            })
                          }
                        >
                          Revert
                        </Button>
                      )}
                    </span>
                  </TableCell>
                  <TableCell className="max-w-72 text-muted-foreground">
                    <span className="block truncate">{a.network?.reason}</span>
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
      )}
    </div>
  )
}
