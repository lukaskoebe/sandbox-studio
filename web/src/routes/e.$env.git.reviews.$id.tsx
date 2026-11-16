import { Link, createFileRoute } from "@tanstack/react-router"
import { GitBranchIcon } from "@phosphor-icons/react"
import {
  GitReview,
  GitReviewHead,
  isGitApproval,
} from "@/components/git-review"
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
import { $api, errorMessage, errorStatus } from "@/lib/api/client"
import { useClaimApprovals } from "@/lib/approval-claims"
import { approvalKind } from "@/lib/approval-kinds"

export const Route = createFileRoute("/e/$env/git/reviews/$id")({
  component: GitReviewPage,
})

/**
 * One git push or pull request review as a page of its own, so it can be linked to: the same
 * review as its computer's Changes tab shows, kept for decided pushes and computers that are gone.
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
        <div className="mx-auto grid max-w-5xl gap-4 text-xs">
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
                      to="/e/$env/activity"
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
              <GitReviewHead approval={a} />
              <GitReview key={a.id} approval={a} />
            </>
          )}
        </div>
      </ScrollArea>
    </>
  )
}
