import { useState } from "react"
import { Link, createFileRoute } from "@tanstack/react-router"
import { GearIcon, GitBranchIcon } from "@phosphor-icons/react"
import { Button } from "@/components/ui/button"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Spinner } from "@/components/ui/spinner"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { KindChip } from "@/components/kind-chip"
import {
  GitOutcome,
  GitReviewSheet,
  isGitApproval,
} from "@/components/git-review"
import { EnvironmentLabel, PageHeader } from "@/components/page-header"
import { SectionHeader } from "@/components/section-header"
import { StatusDot } from "@/components/status-dot"
import { $api, type Approval } from "@/lib/api/client"
import { approvalKind, gitSummary } from "@/lib/approval-kinds"
import { formatAge } from "@/lib/utils"

export const Route = createFileRoute("/e/$env/git/")({
  component: GitPage,
})

/**
 * The pushes and pull requests of the environment's sandboxes, waiting ones first. Each opens
 * its review; the forges they go to are set up in Settings → Git.
 */
function GitPage() {
  const { env } = Route.useParams()
  const [reviewing, setReviewing] = useState<string>()
  const approvals = $api.useQuery(
    "get",
    "/api/environments/{env}/approvals",
    { params: { path: { env }, query: { status: "all" } } },
    { refetchInterval: 5000 }
  )
  const forges = $api.useQuery("get", "/api/environments/{env}/forges", {
    params: { path: { env } },
  })
  const pushes = (approvals.data ?? []).filter(isGitApproval)
  const waiting = pushes.filter((a) => a.status === "pending")
  const decided = pushes.filter((a) => a.status !== "pending")

  return (
    <>
      <PageHeader>
        <h1 className="text-sm font-medium">Git</h1>
        <EnvironmentLabel env={env} />
        <Button
          size="sm"
          variant="outline"
          className="ml-auto"
          render={<Link to="/e/$env/settings/git" params={{ env }} />}
        >
          <GearIcon />
          Forges
        </Button>
      </PageHeader>
      <ScrollArea
        className="min-h-0 flex-1"
        viewportClassName="space-y-6 p-3 md:p-4"
      >
        <p className="max-w-2xl text-xs/relaxed text-muted-foreground">
          Sandboxes push through Studio, which holds each push and pull request
          until you review it here.
        </p>
        {approvals.isPending ? (
          <Spinner className="mx-auto block" />
        ) : pushes.length ? (
          <>
            {waiting.length > 0 && (
              <PushTable
                title="Waiting for you"
                pushes={waiting}
                onReview={setReviewing}
              />
            )}
            {decided.length > 0 && (
              <PushTable
                title="Decided"
                pushes={decided.slice(0, 100)}
                onReview={setReviewing}
              />
            )}
          </>
        ) : (
          <div className="flex flex-wrap items-center gap-3 rounded-(--radius) border border-dashed px-3 py-2.5 text-xs">
            <p className="min-w-0 flex-1 text-muted-foreground">
              {forges.data?.length
                ? "No pushes yet. When a sandbox pushes to one of your forges or opens a pull request, it waits here for your review."
                : "No pushes yet. Add a forge first, such as GitHub or Codeberg; sandboxes then push to it through Studio, and each push waits here for your review."}
            </p>
            {!forges.isPending && !forges.data?.length && (
              <Button
                size="sm"
                variant="outline"
                render={<Link to="/e/$env/settings/git" params={{ env }} />}
              >
                Add a forge
              </Button>
            )}
          </div>
        )}
      </ScrollArea>
      <GitReviewSheet
        approval={pushes.find((a) => a.id === reviewing)}
        onOpenChange={(open) => {
          if (!open) setReviewing(undefined)
        }}
      />
    </>
  )
}

function PushTable({
  title,
  pushes,
  onReview,
}: {
  title: string
  pushes: Approval[]
  onReview: (id: string) => void
}) {
  return (
    <section className="space-y-2">
      <SectionHeader
        title={title}
        icon={GitBranchIcon}
        area="git"
        count={pushes.length}
      />
      <div className="rounded-lg ring-1 ring-foreground/10">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Push</TableHead>
              <TableHead>Sandbox</TableHead>
              <TableHead>Status</TableHead>
              <TableHead>Outcome</TableHead>
              <TableHead>Requested</TableHead>
              <TableHead>
                <span className="sr-only">Actions</span>
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {pushes.map((a) => (
              <PushRow
                key={a.id}
                approval={a}
                onReview={() => onReview(a.id)}
              />
            ))}
          </TableBody>
        </Table>
      </div>
    </section>
  )
}

/** A push or pull request: what it asks, where it stands and the button that reviews it. */
function PushRow({
  approval,
  onReview,
}: {
  approval: Approval
  onReview: () => void
}) {
  const kind = approvalKind(approval)
  const pending = approval.status === "pending"
  return (
    <TableRow>
      <TableCell>
        <div className="flex items-center gap-2">
          <KindChip area={kind.area} icon={kind.icon}>
            {kind.title}
          </KindChip>
          <Link
            to="/e/$env/git/reviews/$id"
            params={{ env: approval.environmentId, id: approval.id }}
            className="max-w-96 truncate underline-offset-4 hover:underline"
          >
            {gitSummary(approval)}
          </Link>
        </div>
      </TableCell>
      <TableCell className="text-muted-foreground">
        {approval.git?.review?.sandbox}
      </TableCell>
      <TableCell>
        {pending ? (
          <StatusDot status="waiting" />
        ) : (
          <span className="capitalize">{approval.status}</span>
        )}
      </TableCell>
      <TableCell>
        {approval.git && !pending && (
          <div className="max-w-72 whitespace-normal">
            <GitOutcome git={approval.git} />
          </div>
        )}
      </TableCell>
      <TableCell className="text-muted-foreground">
        {formatAge(approval.createdAt)}
      </TableCell>
      <TableCell>
        <Button
          size="xs"
          variant={pending ? "default" : "ghost"}
          onClick={onReview}
        >
          {pending ? "Review" : "Show"}
        </Button>
      </TableCell>
    </TableRow>
  )
}
