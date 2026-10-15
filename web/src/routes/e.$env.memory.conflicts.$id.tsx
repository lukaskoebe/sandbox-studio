import { Link, createFileRoute } from "@tanstack/react-router"
import { ScalesIcon } from "@phosphor-icons/react"
import { KindChip } from "@/components/kind-chip"
import { ConflictBody, verdictText } from "@/components/memory-conflict"
import { PageHeader } from "@/components/page-header"
import { Button } from "@/components/ui/button"
import {
  Empty,
  EmptyContent,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Spinner } from "@/components/ui/spinner"
import { usePendingApprovals } from "@/hooks/use-pending-approvals"
import { $api, errorMessage, errorStatus } from "@/lib/api/client"
import { useClaimApprovals } from "@/lib/approval-claims"

export const Route = createFileRoute("/e/$env/memory/conflicts/$id")({
  component: ConflictPage,
})

/**
 * One memory conflict as a page of its own, so it can be linked to: the same view as the sheet
 * the approval card and the Conflicts tab open.
 */
function ConflictPage() {
  const { env, id } = Route.useParams()
  const detail = $api.useQuery(
    "get",
    "/api/environments/{env}/memory/conflicts/{id}",
    { params: { path: { env, id } } },
    { retry: (n, err) => errorStatus(err) !== 404 && n < 2 }
  )
  const c = detail.data
  // The page decides it, so the approval stack leaves its card out.
  const pending = usePendingApprovals()
  useClaimApprovals(
    (pending.data ?? [])
      .filter((a) => a.memoryConflict?.conflictId === id)
      .map((a) => a.id)
  )

  return (
    <>
      <PageHeader>
        <h1 className="text-sm font-medium">Memory conflict</h1>
      </PageHeader>
      <ScrollArea className="min-h-0 flex-1" viewportClassName="p-4">
        <div className="mx-auto grid max-w-3xl gap-4 text-xs">
          {detail.isPending ? (
            <Spinner className="mx-auto block" />
          ) : !c ? (
            <Empty className="border">
              <EmptyHeader>
                <EmptyMedia variant="icon">
                  <ScalesIcon />
                </EmptyMedia>
                <EmptyTitle>No such conflict</EmptyTitle>
                <EmptyDescription>
                  {errorStatus(detail.error) !== 404
                    ? errorMessage(detail.error)
                    : "This conflict isn't in this environment. Its facts may have been deleted."}
                </EmptyDescription>
              </EmptyHeader>
              <EmptyContent>
                <Button
                  variant="outline"
                  size="sm"
                  render={<Link to="/e/$env/memory" params={{ env }} />}
                >
                  Memory
                </Button>
              </EmptyContent>
            </Empty>
          ) : (
            <>
              <div className="grid gap-1.5">
                <KindChip
                  area="memory"
                  icon={ScalesIcon}
                  className="self-start"
                >
                  Memory conflict
                </KindChip>
                <h2 className="text-sm font-medium">Conflicting facts</h2>
                <p className="text-muted-foreground">
                  {verdictText[c.verdict] ?? c.verdict}
                </p>
              </div>
              {c.status !== "open" && (
                <p
                  role="status"
                  className="rounded-md border border-dashed px-3 py-2.5 font-medium"
                >
                  This conflict is resolved
                  {c.resolution && ` (${c.resolution.replaceAll("_", " ")})`}.
                  Nothing is left to decide.
                </p>
              )}
              <ConflictBody
                key={c.id}
                env={env}
                conflict={c}
                onResolved={() => void detail.refetch()}
              />
            </>
          )}
        </div>
      </ScrollArea>
    </>
  )
}
