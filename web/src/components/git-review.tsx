import { useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  GitCommitIcon,
  GitDiffIcon,
  GitPullRequestIcon,
} from "@phosphor-icons/react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { ScrollArea, ScrollBar } from "@/components/ui/scroll-area"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"
import { Spinner } from "@/components/ui/spinner"
import { Ident } from "@/components/ident"
import { KindChip } from "@/components/kind-chip"
import { SectionHeader } from "@/components/section-header"
import { StatusDot } from "@/components/status-dot"
import { textareaClass } from "@/components/memory-dialogs"
import {
  $api,
  errorMessage,
  errorStatus,
  type Approval,
  type GitDetail,
} from "@/lib/api/client"
import { approvalKind, gitSummary } from "@/lib/approval-kinds"
import { cn, formatAge } from "@/lib/utils"

type Action = "allow" | "deny" | "dismiss"

export function isGitApproval(a: Approval) {
  return a.kind === "git.push" || a.kind === "git.pr"
}

function short(sha: string) {
  return sha.slice(0, 10)
}

function useDecideGit() {
  const queryClient = useQueryClient()
  return $api.useMutation(
    "post",
    "/api/environments/{env}/approvals/{id}/decide",
    {
      onSettled: () => {
        queryClient.invalidateQueries({ queryKey: ["get", "/api/approvals"] })
        queryClient.invalidateQueries({
          queryKey: ["get", "/api/environments/{env}/approvals"],
        })
      },
      onError: (err) => {
        if (errorStatus(err) === 409) return
        toast.error("Could not decide", { description: errorMessage(err) })
      },
    }
  )
}

/** The state of a decided push or pull request, for lists. */
export function GitOutcome({ git }: { git: GitDetail }) {
  const p = git.push
  if (git.pullRequest) {
    if (p.prUrl)
      return (
        <a
          href={p.prUrl}
          target="_blank"
          rel="noreferrer"
          className="text-xs underline underline-offset-4"
        >
          Pull request opened
        </a>
      )
    return p.prResult ? (
      <span className="text-xs text-muted-foreground">{p.prResult}</span>
    ) : null
  }
  return (
    <span className="flex min-w-0 items-center gap-1.5 text-xs">
      {p.state === "failed" ? (
        <StatusDot status="failed" />
      ) : (
        <span className="capitalize">{p.state}</span>
      )}
      {(p.result || p.note) && (
        <span className="truncate text-muted-foreground">
          {[p.result, p.note && `Note: ${p.note}`].filter(Boolean).join(" · ")}
        </span>
      )}
    </span>
  )
}

/**
 * The full review of a git approval: commits and diff of a push, or the proposed pull
 * request, with a note and the decision buttons while it is pending.
 */
export function GitReview({ approval }: { approval: Approval }) {
  const git = approval.git
  const [note, setNote] = useState("")
  const decide = useDecideGit()
  if (!git) return null
  const pending = approval.status === "pending"
  const busy = (action: Action) =>
    decide.isPending && decide.variables.body.action === action
  const act = (action: Action) =>
    decide.mutate({
      params: { path: { env: approval.environmentId, id: approval.id } },
      // Scope only matters for network approvals.
      body: { action, scope: "sandbox", note: note.trim() || undefined },
    })
  const rv = git.review
  const pr = git.pullRequest
  const branch = pr ? `${pr.head} → ${pr.base}` : rv?.branch

  return (
    <div className="grid gap-4">
      <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-xs">
        <dt className="text-muted-foreground">Repository</dt>
        <dd>
          <Ident>{`${git.push.forgeName}/${git.push.owner}/${git.push.repo}`}</Ident>
        </dd>
        <dt className="text-muted-foreground">Sandbox</dt>
        <dd>
          {rv?.sandbox ?? <Ident>{git.push.sandboxId}</Ident>}
          {git.push.teammateName && (
            <span className="text-muted-foreground">
              {" "}
              · teammate {git.push.teammateName}
            </span>
          )}
        </dd>
        <dt className="text-muted-foreground">Branch</dt>
        <dd>
          {branch && <Ident>{branch}</Ident>}
          {rv && !rv.old && (
            <Badge variant="outline" className="ml-1.5 font-sans">
              new branch
            </Badge>
          )}
        </dd>
        {rv && (
          <>
            <dt className="text-muted-foreground">Change</dt>
            <dd>
              <Ident>{`${rv.old ? short(rv.old) : "∅"} → ${short(rv.new)}`}</Ident>{" "}
              · {rv.files} {rv.files === 1 ? "file" : "files"}{" "}
              <span className="text-emerald-600 dark:text-emerald-400">
                +{rv.additions}
              </span>{" "}
              <span className="text-destructive">−{rv.deletions}</span>
            </dd>
          </>
        )}
        {!pending && (
          <>
            <dt className="text-muted-foreground">Outcome</dt>
            <dd className="min-w-0">
              <GitOutcome git={git} />
            </dd>
          </>
        )}
      </dl>

      {pr && (
        <section className="grid gap-2">
          <SectionHeader
            title="Pull request"
            icon={GitPullRequestIcon}
            area="git"
          />
          <p className="text-xs font-medium">{pr.title}</p>
          {pr.body && (
            <ScrollArea
              className="rounded-md bg-muted/50"
              viewportClassName="max-h-60"
            >
              <pre className="p-2 text-xs/relaxed whitespace-pre-wrap">
                {pr.body}
              </pre>
            </ScrollArea>
          )}
        </section>
      )}

      {rv && (
        <section className="grid gap-2">
          <SectionHeader
            title="Commits"
            icon={GitCommitIcon}
            area="git"
            count={(rv.commits?.length ?? 0) + rv.moreCommits}
          />
          <ul className="divide-y rounded-md text-xs ring-1 ring-foreground/10">
            {(rv.commits ?? []).map((c) => (
              <li key={c.sha} className="grid gap-0.5 px-2 py-1.5">
                <div className="flex min-w-0 items-baseline gap-2">
                  <Ident copy value={c.sha} className="text-muted-foreground">
                    {short(c.sha)}
                  </Ident>
                  <span className="truncate font-medium">{c.subject}</span>
                  <span className="ml-auto shrink-0 text-muted-foreground">
                    {c.author} · {formatAge(c.when)}
                  </span>
                </div>
                {c.body && (
                  <p className="whitespace-pre-wrap text-muted-foreground">
                    {c.body}
                  </p>
                )}
              </li>
            ))}
            {rv.moreCommits > 0 && (
              <li className="px-2 py-1.5 text-muted-foreground">
                and {rv.moreCommits} more
              </li>
            )}
          </ul>
        </section>
      )}

      {rv && (
        <section className="grid min-w-0 gap-2">
          <SectionHeader
            title="Diff"
            icon={GitDiffIcon}
            area="git"
            actions={
              rv.diffTruncated && <Badge variant="outline">truncated</Badge>
            }
          />
          <UnifiedDiff diff={rv.diff} />
        </section>
      )}

      {pending && (
        <div className="grid gap-2">
          <textarea
            aria-label="Note for the agent"
            placeholder="Note for the agent (shown when it fetches after a rejection)"
            rows={2}
            maxLength={2000}
            value={note}
            onChange={(e) => setNote(e.target.value)}
            className={textareaClass}
          />
          <div className="flex items-center gap-1.5">
            <Button
              variant="ghost"
              size="xs"
              disabled={decide.isPending}
              onClick={() => act("dismiss")}
            >
              {busy("dismiss") && <Spinner className="size-3" />}
              Ignore
            </Button>
            <Button
              variant="outline"
              size="sm"
              className="ml-auto"
              disabled={decide.isPending}
              onClick={() => act("deny")}
            >
              {busy("deny") && <Spinner />}
              Reject
            </Button>
            <Button
              size="sm"
              disabled={decide.isPending}
              onClick={() => act("allow")}
            >
              {busy("allow") && <Spinner />}
              {pr ? "Open pull request" : "Approve and push"}
            </Button>
          </div>
        </div>
      )}
    </div>
  )
}

function lineClass(line: string) {
  if (line.startsWith("+++") || line.startsWith("---"))
    return "text-muted-foreground"
  if (line.startsWith("+"))
    return "bg-emerald-500/10 text-emerald-700 dark:text-emerald-300"
  if (line.startsWith("-")) return "bg-destructive/10 text-destructive"
  if (line.startsWith("@@")) return "text-sky-600 dark:text-sky-400"
  if (line.startsWith("diff --git"))
    return "mt-2 font-semibold text-foreground first:mt-0"
  if (line.startsWith("[")) return "text-amber-600 dark:text-amber-400"
  return ""
}

/** A unified diff, colored line by line. */
export function UnifiedDiff({ diff }: { diff: string }) {
  if (!diff) return <p className="text-xs text-muted-foreground">No changes.</p>
  return (
    <ScrollArea
      className="rounded-md bg-muted/40 ring-1 ring-foreground/10"
      viewportClassName="max-h-[60vh]"
    >
      <pre className="py-2 font-mono text-[11px]/[1.45]">
        {diff.split("\n").map((line, i) => (
          <div key={i} className={cn("px-2 whitespace-pre", lineClass(line))}>
            {line || " "}
          </div>
        ))}
      </pre>
      <ScrollBar orientation="horizontal" />
    </ScrollArea>
  )
}

/** A wide sheet with the review of one git approval. */
export function GitReviewSheet({
  approval,
  onOpenChange,
}: {
  approval?: Approval
  onOpenChange: (open: boolean) => void
}) {
  const kind = approval && approvalKind(approval)
  return (
    <Sheet open={approval !== undefined} onOpenChange={onOpenChange}>
      <SheetContent className="data-[side=right]:sm:max-w-3xl">
        {approval && kind && (
          <>
            <SheetHeader>
              <KindChip
                area={kind.area}
                icon={kind.icon}
                className="self-start"
              >
                {kind.title}
              </KindChip>
              <SheetTitle>{gitSummary(approval)}</SheetTitle>
              <SheetDescription>
                {approval.git?.pullRequest
                  ? "The push was approved and is on the forge. Approving opens this pull request."
                  : "The sandbox pushed to Studio's git remote. Nothing reaches the forge until you approve; a rejection is shown to the agent on its next fetch."}
                {!approval.git?.pullRequest &&
                  approval.git?.review?.upstream &&
                  ` Approving pushes it ${approval.git.review.upstream}.`}
              </SheetDescription>
            </SheetHeader>
            <ScrollArea
              className="min-h-0 flex-1"
              viewportClassName="px-6 pb-6"
            >
              <GitReview key={approval.id} approval={approval} />
            </ScrollArea>
          </>
        )}
      </SheetContent>
    </Sheet>
  )
}

/** A compact card body for the approval stack: what is asked, and a button to review it. */
export function GitApprovalSummary({
  approval,
  onReview,
}: {
  approval: Approval
  onReview: () => void
}) {
  const rv = approval.git?.review
  return (
    <div className="flex items-center gap-2 text-xs">
      {rv ? (
        <span className="text-muted-foreground">
          {rv.files} {rv.files === 1 ? "file" : "files"}{" "}
          <span className="text-emerald-600 dark:text-emerald-400">
            +{rv.additions}
          </span>{" "}
          <span className="text-destructive">−{rv.deletions}</span>
        </span>
      ) : (
        <span className="truncate text-muted-foreground">
          {approval.git?.pullRequest?.title}
        </span>
      )}
      <Button size="sm" className="ml-auto" onClick={onReview}>
        Review
      </Button>
    </div>
  )
}
