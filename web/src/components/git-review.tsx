import { useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { Link } from "@tanstack/react-router"
import { toast } from "sonner"
import {
  ArrowSquareOutIcon,
  GitCommitIcon,
  GitDiffIcon,
  GitPullRequestIcon,
} from "@phosphor-icons/react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import { ScrollArea } from "@/components/ui/scroll-area"
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
  FeedbackTargetSelect,
  UnifiedDiff,
  composeNote,
  feedbackBody,
  newChatTarget,
  parseDiff,
  useSendFeedback,
  type FeedbackTarget,
  type LineComment,
} from "@/components/review-feedback"
import {
  $api,
  errorMessage,
  errorStatus,
  type Approval,
  type GitDetail,
} from "@/lib/api/client"
import { approvalKind, gitSummary } from "@/lib/approval-kinds"
import { formatAge } from "@/lib/utils"

type Action = "allow" | "deny" | "dismiss"

export function isGitApproval(a: Approval) {
  return a.kind === "git.push" || a.kind === "git.pr"
}

function short(sha: string) {
  return sha.slice(0, 10)
}

function useInvalidateApprovals() {
  const queryClient = useQueryClient()
  return () => {
    queryClient.invalidateQueries({ queryKey: ["get", "/api/approvals"] })
    queryClient.invalidateQueries({
      queryKey: ["get", "/api/environments/{env}/approvals"],
    })
    queryClient.invalidateQueries({
      queryKey: ["get", "/api/environments/{env}/approvals/{id}"],
    })
  }
}

function useDecideGit() {
  const invalidate = useInvalidateApprovals()
  return $api.useMutation(
    "post",
    "/api/environments/{env}/approvals/{id}/decide",
    {
      onSettled: invalidate,
      onError: (err) => {
        if (errorStatus(err) === 409) return
        toast.error("Could not decide", { description: errorMessage(err) })
      },
    }
  )
}

/** Opens the pull request of a push approved without one. */
function useOpenPullRequest() {
  const invalidate = useInvalidateApprovals()
  return $api.useMutation(
    "post",
    "/api/environments/{env}/approvals/{id}/pull-request",
    {
      onSettled: invalidate,
      onSuccess: () => toast.success("Pull request opened"),
      onError: (err) =>
        toast.error("Could not open the pull request", {
          description: errorMessage(err),
        }),
    }
  )
}

/** Whether a decided push can still have its pull request opened. */
function canOpenLater(git: GitDetail) {
  const p = git.push
  return (
    !!git.review?.pullRequest &&
    p.state === "pushed" &&
    !p.prUrl &&
    (!p.prApprovalId || !!p.prResult)
  )
}

function PullRequestLink({ url, children }: { url: string; children: string }) {
  return url.startsWith("http") ? (
    <a
      href={url}
      target="_blank"
      rel="noreferrer"
      className="text-xs underline underline-offset-4"
    >
      {children}
    </a>
  ) : (
    <span className="text-xs">
      {children} <Ident>{url}</Ident>
    </span>
  )
}

/** The state of a decided push or pull request, for lists. */
export function GitOutcome({ git }: { git: GitDetail }) {
  const p = git.push
  if (git.pullRequest) {
    if (p.prUrl)
      return (
        <PullRequestLink url={p.prUrl}>Pull request opened</PullRequestLink>
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
      {p.prUrl && (
        <span className="shrink-0">
          ·{" "}
          <PullRequestLink url={p.prUrl}>
            {p.prResult === "updated" ? "Pull request updated" : "Pull request"}
          </PullRequestLink>
        </span>
      )}
      {!p.prUrl && p.prResult?.startsWith("failed") && (
        <span className="truncate text-muted-foreground">· {p.prResult}</span>
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
  const [comments, setComments] = useState<LineComment[]>([])
  const review = git?.review
  const proposal = review?.pullRequest
  const [target, setTarget] = useState<FeedbackTarget>(
    git?.push.teammateName ? newChatTarget : ""
  )
  // A new branch opens its pull request by default; a push to a branch that has been
  // pushed before but has none doesn't.
  const [openPR, setOpenPR] = useState(!!review?.pullRequest && !review.old)
  const [prTitle, setPRTitle] = useState(proposal?.title ?? "")
  const decide = useDecideGit()
  const openLater = useOpenPullRequest()
  const feedback = useSendFeedback()
  if (!git) return null
  const pending = approval.status === "pending"
  const params = {
    params: { path: { env: approval.environmentId, id: approval.id } },
  }
  const fullNote = () =>
    composeNote(note, parseDiff(git.review?.diff ?? ""), comments)
  const send = () =>
    feedback.mutate({ ...params, body: feedbackBody(target, fullNote()) })
  const busy = (action: Action) =>
    decide.isPending && decide.variables.body.action === action
  // A new chat starts only when there is something to tell it beyond the outcome.
  const sends = () => !!target && (target !== newChatTarget || !!fullNote())
  const act = (action: Action) =>
    decide.mutate(
      {
        ...params,
        body: {
          action,
          // Scope only matters for network approvals.
          scope: "sandbox",
          note: fullNote() || undefined,
          pullRequest:
            proposal && action === "allow"
              ? { open: openPR, title: openPR ? prTitle.trim() : undefined }
              : undefined,
        },
      },
      { onSuccess: () => sends() && send() }
    )
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

      {rv?.openPullRequest && (
        <p className="flex items-center gap-1.5 text-xs text-muted-foreground">
          <GitPullRequestIcon className="size-3.5" />
          The branch has an open pull request; the push updates it.
          <PullRequestLink url={rv.openPullRequest}>Open it</PullRequestLink>
        </p>
      )}

      {proposal && pending && (
        <section className="grid gap-2 rounded-md p-2 text-xs ring-1 ring-foreground/10">
          <label className="flex items-center gap-2">
            <Checkbox checked={openPR} onCheckedChange={setOpenPR} />
            Also open a pull request into <Ident>{proposal.base}</Ident>
          </label>
          {openPR && (
            <Input
              aria-label="Pull request title"
              value={prTitle}
              maxLength={200}
              onChange={(e) => setPRTitle(e.target.value)}
              className="h-7 text-xs"
            />
          )}
          {!openPR && (
            <p className="text-muted-foreground">
              Approving only pushes; you can open the pull request later.
            </p>
          )}
        </section>
      )}

      {!pending && canOpenLater(git) && (
        <div className="flex items-center gap-2 text-xs text-muted-foreground">
          <GitPullRequestIcon className="size-3.5" />
          {git.push.prResult
            ? "The pull request was not opened."
            : "Pushed without a pull request."}
          <Button
            variant="outline"
            size="xs"
            className="ml-auto"
            disabled={openLater.isPending}
            onClick={() => openLater.mutate({ ...params, body: {} })}
          >
            {openLater.isPending && <Spinner className="size-3" />}
            Open pull request
          </Button>
        </div>
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
          <UnifiedDiff
            diff={rv.diff}
            comments={comments}
            onComment={pending ? setComments : undefined}
          />
        </section>
      )}

      {pending && (
        <div className="grid gap-2">
          <textarea
            aria-label="Note for the agent"
            placeholder={
              rv
                ? "Note for the agent. Comment on a line of the diff with its button."
                : "Note for the agent"
            }
            rows={2}
            maxLength={4000}
            value={note}
            onChange={(e) => setNote(e.target.value)}
            className={textareaClass}
          />
          {approval.sandboxId && (
            <FeedbackTargetSelect
              env={approval.environmentId}
              sandboxId={approval.sandboxId}
              newChat={!!git.push.teammateName}
              value={target}
              onChange={setTarget}
            />
          )}
          <p className="text-xs text-muted-foreground">
            {target === newChatTarget
              ? "With a note or line comments, deciding starts a chat in the sandbox that tells the agent the outcome and your comments."
              : target
                ? "Deciding sends the outcome, the note and your line comments there."
                : "The agent sees a rejection and its note when it next fetches or gets a prompt."}
          </p>
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
            {target && (
              <Button
                variant="ghost"
                size="xs"
                disabled={feedback.isPending || !fullNote()}
                title="Send the note and comments, and keep the push waiting"
                onClick={send}
              >
                {feedback.isPending && !decide.isPending && (
                  <Spinner className="size-3" />
                )}
                Send without deciding
              </Button>
            )}
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
              disabled={
                decide.isPending || (!!proposal && openPR && !prTitle.trim())
              }
              onClick={() => act("allow")}
            >
              {busy("allow") && <Spinner />}
              {pr
                ? "Open pull request"
                : proposal && openPR
                  ? "Push and open pull request"
                  : "Approve and push"}
            </Button>
          </div>
        </div>
      )}
    </div>
  )
}

/** What reviewing a git approval decides, under its title. */
export function gitReviewDescription(approval: Approval) {
  const git = approval.git
  if (git?.pullRequest)
    return "The push was approved and is on the forge. Approving opens this pull request."
  const upstream = git?.review?.upstream
  return (
    "The sandbox pushed to Studio's git remote. Nothing reaches the forge until you approve; a rejection is shown to the agent on its next fetch." +
    (upstream ? ` Approving pushes it ${upstream}` : " Approving pushes it") +
    (git?.review?.pullRequest
      ? ", and can open a pull request with it."
      : git?.review?.openPullRequest
        ? ", which updates the branch's open pull request."
        : ".")
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
              <div className="flex items-center gap-2 pr-8">
                <KindChip area={kind.area} icon={kind.icon}>
                  {kind.title}
                </KindChip>
                <Button
                  variant="ghost"
                  size="xs"
                  className="ml-auto"
                  render={
                    <Link
                      to="/e/$env/git/reviews/$id"
                      params={{ env: approval.environmentId, id: approval.id }}
                      onClick={() => onOpenChange(false)}
                    />
                  }
                >
                  <ArrowSquareOutIcon />
                  Open as page
                </Button>
              </div>
              <SheetTitle>{gitSummary(approval)}</SheetTitle>
              <SheetDescription>
                {gitReviewDescription(approval)}
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
