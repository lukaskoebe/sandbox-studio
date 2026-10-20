import { Link, createFileRoute } from "@tanstack/react-router"
import { GitBranchIcon } from "@phosphor-icons/react"
import {
  GitReview,
  gitReviewDescription,
  isGitApproval,
} from "@/components/git-review"
import { KindChip } from "@/components/kind-chip"
import { PageHeader } from "@/components/page-header"
import { StatusDot } from "@/components/status-dot"
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
import { $api, errorMessage, errorStatus } from "@/lib/api/client"
import { useClaimApprovals } from "@/lib/approval-claims"
import { approvalKind, gitSummary } from "@/lib/approval-kinds"
import { formatAge } from "@/lib/utils"

export const Route = createFileRoute("/e/$env/git/reviews/$id")({
  component: GitReviewPage,
})

/** What a decided push or pull request came to, in one sentence. */
const decided: Record<string, string> = {
  approved: "This was approved.",
  denied: "This was rejected.",
  dismissed: "This was ignored.",
}

/**
 * One git push or pull request review as a page of its own, so it can be linked to: the same
 * review as the sheet the approval card opens.
 */
function GitReviewPage() {
  const { env, id } = Route.useParams()
  const approval = $api.useQuery(
    "get",
    "/api/environments/{env}/approvals/{id}",
    { params: { path: { env, id } } },
    { retry: (n, err) => errorStatus(err) !== 404 && n < 2 }
  )
  const a = approval.data
  const kind = a && approvalKind(a)
  // The page decides it, so the approval stack leaves its card out.
  useClaimApprovals(a?.status === "pending" ? [a.id] : [])

  return (
    <>
      <PageHeader>
        <h1 className="text-sm font-medium">Git review</h1>
      </PageHeader>
      <ScrollArea className="min-h-0 flex-1" viewportClassName="p-4">
        <div className="mx-auto grid max-w-3xl gap-4 text-xs">
          {approval.isPending ? (
            <Spinner className="mx-auto block" />
          ) : !a || !kind || !isGitApproval(a) ? (
            <Empty className="border">
              <EmptyHeader>
                <EmptyMedia variant="icon">
                  <GitBranchIcon />
                </EmptyMedia>
                <EmptyTitle>No such review</EmptyTitle>
                <EmptyDescription>
                  {approval.isError && errorStatus(approval.error) !== 404
                    ? errorMessage(approval.error)
                    : "This push or pull request isn't in this environment. Its sandbox may have been deleted."}
                </EmptyDescription>
              </EmptyHeader>
              <EmptyContent>
                <Button
                  variant="outline"
                  size="sm"
                  render={
                    <Link
                      to="/e/$env/settings/activity"
                      params={{ env }}
                      search={{ kind: "git" }}
                    />
                  }
                >
                  All pushes
                </Button>
              </EmptyContent>
            </Empty>
          ) : (
            <>
              <div className="grid gap-1.5">
                <div className="flex flex-wrap items-center gap-2">
                  <KindChip area={kind.area} icon={kind.icon}>
                    {kind.title}
                  </KindChip>
                  {a.status === "pending" && <StatusDot status="waiting" />}
                  <span className="text-muted-foreground">
                    {formatAge(a.createdAt)}
                  </span>
                </div>
                <h2 className="text-sm font-medium break-words">
                  {gitSummary(a)}
                </h2>
                {a.status === "pending" && (
                  <p className="text-muted-foreground">
                    {gitReviewDescription(a)}
                  </p>
                )}
              </div>
              {a.status !== "pending" && (
                <p
                  role="status"
                  className="rounded-md border border-dashed px-3 py-2.5 font-medium"
                >
                  {decided[a.status] ?? `This is ${a.status}.`} Nothing is left
                  to decide; the outcome is below.
                </p>
              )}
              <GitReview key={a.id} approval={a} />
            </>
          )}
        </div>
      </ScrollArea>
    </>
  )
}
