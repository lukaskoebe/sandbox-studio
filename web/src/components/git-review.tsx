import { useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { GitBranchIcon, GitPullRequestIcon } from "@phosphor-icons/react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"
import { Spinner } from "@/components/ui/spinner"
import { textareaClass } from "@/components/memory-dialogs"
import {
  $api,
  errorMessage,
  errorStatus,
  type Approval,
  type GitDetail,
} from "@/lib/api/client"
import { cn, formatAge } from "@/lib/utils"

type Action = "allow" | "deny" | "dismiss"

export function isGitApproval(a: Approval) {
  return a.kind === "git.push" || a.kind === "git.pr"
}

function short(sha: string) {
  return sha.slice(0, 10)
}

/** One line describing what a git approval asks for. */
export function gitSummary(a: Approval): string {
  const git = a.git
  if (!git) return a.subject
  const where = `${git.push.owner}/${git.push.repo}`
  if (git.pullRequest) {
    return `Open a pull request ${git.pullRequest.head} → ${git.pullRequest.base} on ${where}`
  }
  const commits =
    (git.review?.commits?.length ?? 0) + (git.review?.moreCommits ?? 0)
  const branch =
    git.review?.branch ?? git.push.ref.replace(/^refs\/heads\//, "")
  return `Push ${commits} ${commits === 1 ? "commit" : "commits"} to ${branch} on ${where}`
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
  const variant =
    p.state === "pushed"
      ? "secondary"
      : p.state === "failed"
        ? "destructive"
        : "outline"
  return (
    <span className="flex min-w-0 items-center gap-1.5 text-xs">
      <Badge variant={variant}>{p.state}</Badge>
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

  return (
    <div className="grid gap-4">
      <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-xs">
        <dt className="text-muted-foreground">Repository</dt>
        <dd className="font-mono">
          {git.push.forgeName}/{git.push.owner}/{git.push.repo}
        </dd>
        <dt className="text-muted-foreground">Sandbox</dt>
        <dd>
          {rv?.sandbox ?? git.push.sandboxId}
          {git.push.personaName && (
            <span className="text-muted-foreground">
              {" "}
              · persona {git.push.personaName}
            </span>
          )}
        </dd>
        <dt className="text-muted-foreground">Branch</dt>
        <dd className="font-mono">
          {pr ? `${pr.head} → ${pr.base}` : rv?.branch}
          {rv && !rv.old && (
            <Badge variant="outline" className="ml-1.5 font-sans">
              new branch
            </Badge>
          )}
        </dd>
        {rv && (
          <>
            <dt className="text-muted-foreground">Change</dt>
            <dd className="font-mono">
              {rv.old ? short(rv.old) : "∅"} → {short(rv.new)} · {rv.files}{" "}
              {rv.files === 1 ? "file" : "files"}{" "}
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
        <section className="grid gap-1.5">
          <h3 className="text-xs font-medium">{pr.title}</h3>
          {pr.body && (
            <pre className="max-h-60 overflow-auto rounded-md bg-muted/50 p-2 text-xs/relaxed whitespace-pre-wrap">
              {pr.body}
            </pre>
          )}
        </section>
      )}

      {rv && (
        <section className="grid gap-1.5">
          <h3 className="text-xs font-medium">Commits</h3>
          <ul className="divide-y rounded-md text-xs ring-1 ring-foreground/10">
            {(rv.commits ?? []).map((c) => (
              <li key={c.sha} className="grid gap-0.5 px-2 py-1.5">
                <div className="flex min-w-0 items-baseline gap-2">
                  <span className="font-mono text-muted-foreground">
                    {short(c.sha)}
                  </span>
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
        <section className="grid min-w-0 gap-1.5">
          <h3 className="flex items-center gap-2 text-xs font-medium">
            Diff
            {rv.diffTruncated && <Badge variant="outline">truncated</Badge>}
          </h3>
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
    <pre className="max-h-[60vh] overflow-auto rounded-md bg-muted/40 py-2 font-mono text-[11px]/[1.45] ring-1 ring-foreground/10">
      {diff.split("\n").map((line, i) => (
        <div key={i} className={cn("px-2 whitespace-pre", lineClass(line))}>
          {line || " "}
        </div>
      ))}
    </pre>
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
  return (
    <Sheet open={approval !== undefined} onOpenChange={onOpenChange}>
      <SheetContent className="data-[side=right]:sm:max-w-3xl">
        {approval && (
          <>
            <SheetHeader>
              <SheetTitle className="flex items-center gap-2">
                {approval.git?.pullRequest ? (
                  <GitPullRequestIcon />
                ) : (
                  <GitBranchIcon />
                )}
                {gitSummary(approval)}
              </SheetTitle>
              <SheetDescription>
                {approval.git?.pullRequest
                  ? "The push was approved and is on the forge. Approving opens this pull request."
                  : "The sandbox pushed to Studio's git remote. Nothing reaches the forge until you approve; a rejection is shown to the agent on its next fetch."}
              </SheetDescription>
            </SheetHeader>
            <div className="min-h-0 flex-1 overflow-auto px-6 pb-6">
              <GitReview key={approval.id} approval={approval} />
            </div>
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
