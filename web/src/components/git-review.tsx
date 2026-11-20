import {
  ForgeComment,
  ForgeCommentSummary,
  forgeCommentDescription,
} from "@/components/forge-comment"
import { useRef, useState, type ReactNode } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { useMatch, useNavigate } from "@tanstack/react-router"
import { toast } from "sonner"
import {
  ChatCircleTextIcon,
  DotsThreeIcon,
  EyeSlashIcon,
  GitPullRequestIcon,
  PaperPlaneRightIcon,
  WarningIcon,
} from "@phosphor-icons/react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Input } from "@/components/ui/input"
import { ScrollArea } from "@/components/ui/scroll-area"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Spinner } from "@/components/ui/spinner"
import {
  ForgeReview,
  ForgeReviewSummary,
  forgeReviewDescription,
  rememberAttribution,
  rememberedAttribution,
} from "@/components/forge-review"
import { Ident } from "@/components/ident"
import { KindChip } from "@/components/kind-chip"
import { SectionHeader } from "@/components/section-header"
import { StatusDot } from "@/components/status-dot"
import type { DiffViewHandle } from "@/components/diff-view"
import { DiffStyleSwitch } from "@/components/diff-style-switch"
import { textareaClass } from "@/components/memory-dialogs"
import {
  DecisionRow,
  CommentNav,
  DiffFrameText,
  FeedbackTargetSelect,
  ReviewKeys,
  ReviewScroll,
  UnifiedDiff,
  ViewedCount,
  reviewPaneClass,
  commentWhere,
  composeNote,
  feedbackBody,
  newChatTarget,
  parseDiff,
  useSendFeedback,
  type FeedbackTarget,
  type LineComment,
  type ViewedProgress,
} from "@/components/review-feedback"
import {
  $api,
  errorMessage,
  errorStatus,
  type Approval,
  type GitDetail,
} from "@/lib/api/client"
import { approvalKind, gitSummary, plural } from "@/lib/approval-kinds"
import { draftBase, useReviewDraft } from "@/lib/review-drafts"
import { cn, formatAge } from "@/lib/utils"
import { viewedScope } from "@/lib/viewed-files"

type Action = "allow" | "deny" | "dismiss"

type Commit = NonNullable<
  NonNullable<NonNullable<GitDetail["review"]>["commits"]>
>[number]

/** Narrows the diff to one of the push's commits, or shows them all; one commit is named. */
function CommitPicker({
  commits,
  more,
  value,
  onChange,
}: {
  commits: Commit[]
  more: number
  value: string
  onChange: (sha: string) => void
}) {
  if (!commits.length) return null
  if (commits.length === 1 && !more)
    return (
      <span className="flex min-w-0 items-baseline gap-1.5">
        <Ident copy value={commits[0].sha} className="text-muted-foreground">
          {short(commits[0].sha)}
        </Ident>
        <span className="truncate">{commits[0].subject}</span>
      </span>
    )
  const all = `All ${commits.length + more} commits`
  const items: Record<string, string> = { "": all }
  for (const c of commits) items[c.sha] = `${short(c.sha)} ${c.subject}`
  return (
    <Select
      value={value}
      items={items}
      onValueChange={(v) => onChange(v ?? "")}
    >
      <SelectTrigger
        size="sm"
        aria-label="Commits"
        className="max-w-80 min-w-0"
      >
        <SelectValue />
      </SelectTrigger>
      <SelectContent
        alignItemWithTrigger={false}
        align="start"
        className="w-auto max-w-[min(28rem,calc(100vw-2rem))] min-w-(--anchor-width)"
      >
        <SelectItem value="">{all}</SelectItem>
        {commits.map((c) => (
          <SelectItem key={c.sha} value={c.sha}>
            <span className="grid min-w-0">
              <span className="flex min-w-0 items-baseline gap-1.5">
                <Ident className="text-muted-foreground">{short(c.sha)}</Ident>
                <span className="truncate">{c.subject}</span>
              </span>
              <span className="text-caption text-muted-foreground">
                {c.author} · {formatAge(c.when)}
              </span>
            </span>
          </SelectItem>
        ))}
        {more > 0 && (
          <p className="px-2 py-1.5 text-xs text-muted-foreground">
            and {more} more, not listed
          </p>
        )}
      </SelectContent>
    </Select>
  )
}

/** One commit's own diff in the review's frame, with its message above the first file. */
function CommitDiff({
  env,
  approvalId,
  commit: c,
  pending,
  onAll,
}: {
  env: string
  approvalId: string
  commit: Commit
  pending: boolean
  onAll: () => void
}) {
  const q = $api.useQuery(
    "get",
    "/api/environments/{env}/approvals/{id}/commits/{sha}",
    { params: { path: { env, id: approvalId, sha: c.sha } } },
    { staleTime: Infinity, retry: false }
  )
  const d = q.data
  const head = (
    <section aria-label={`Commit ${short(c.sha)}`} className="grid gap-1.5">
      <p className="flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-1">
        <Ident copy value={c.sha} className="text-muted-foreground">
          {short(c.sha)}
        </Ident>
        <span className="min-w-0 text-sm font-medium break-words">
          {c.subject}
        </span>
        <span className="ml-auto shrink-0 text-muted-foreground">
          {c.author} · {formatAge(c.when)}
        </span>
      </p>
      {c.body && (
        <p className="break-words whitespace-pre-wrap text-muted-foreground">
          {c.body}
        </p>
      )}
      <p className="flex flex-wrap items-center gap-x-2 gap-y-1 text-muted-foreground">
        {d && (
          <span>
            {d.files} {d.files === 1 ? "file" : "files"} ·{" "}
            <span className="text-emerald-600 dark:text-emerald-400">
              +{d.additions}
            </span>{" "}
            <span className="text-destructive">−{d.deletions}</span>
          </span>
        )}
        {d?.diffTruncated && <Badge variant="outline">diff truncated</Badge>}
        <span>
          One commit of the push
          {pending && ", without line comments: they go on"}{" "}
          <Button
            variant="link"
            size="xs"
            className="h-auto p-0"
            onClick={onAll}
          >
            {pending ? "all its commits" : "Show all its commits"}
          </Button>
        </span>
      </p>
    </section>
  )
  if (q.isPending)
    return (
      <DiffFrameText header={head}>
        <p className="text-muted-foreground">Loading the changes…</p>
      </DiffFrameText>
    )
  if (q.isError)
    return (
      <DiffFrameText header={head}>
        <p className="text-muted-foreground">
          {errorStatus(q.error) === 404
            ? "Studio no longer keeps this commit."
            : `Could not load the changes: ${errorMessage(q.error)}`}
        </p>
      </DiffFrameText>
    )
  return <UnifiedDiff diff={q.data.diff} fill header={head} />
}

export function isGitApproval(a: Approval) {
  return (
    a.kind === "git.push" ||
    a.kind === "git.pr" ||
    a.kind === "git.review" ||
    a.kind === "git.comment"
  )
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

/**
 * Tells how an approved push went upstream: the approval closes either way, so a failure would
 * otherwise go unseen. A failure stays until it's dismissed and links to the review, which can
 * push again.
 */
function usePushOutcome() {
  const navigate = useNavigate()
  return (a: Approval) => {
    const p = a.git?.push
    if (!p || a.git?.pullRequest) return
    const branch = p.ref.replace(/^refs\/heads\//, "")
    const where = `${p.owner}/${p.repo}`
    if (p.state === "pushed") {
      toast.success(`Pushed ${branch} to ${where}`)
    } else if (p.state === "failed") {
      toast.error(`The push of ${branch} to ${where} failed`, {
        id: `push-failed-${a.id}`,
        description: p.result,
        duration: Infinity,
        closeButton: true,
        action: {
          label: "Open",
          onClick: () =>
            navigate({
              to: "/e/$env/git/reviews/$id",
              params: { env: a.environmentId, id: a.id },
            }),
        },
      })
    }
  }
}

function useDecideGit() {
  const invalidate = useInvalidateApprovals()
  const outcome = usePushOutcome()
  return $api.useMutation(
    "post",
    "/api/environments/{env}/approvals/{id}/decide",
    {
      onSettled: invalidate,
      onSuccess: (a, vars) => {
        if (vars.body.action === "allow") outcome(a)
      },
      onError: (err) => {
        if (errorStatus(err) === 409) return
        toast.error("Could not decide", { description: errorMessage(err) })
      },
    }
  )
}

/** Pushes an approved push that failed upstream again. */
function usePushAgain() {
  const invalidate = useInvalidateApprovals()
  const outcome = usePushOutcome()
  return $api.useMutation(
    "post",
    "/api/environments/{env}/approvals/{id}/push-again",
    {
      onSettled: invalidate,
      onSuccess: (a) => {
        if (a.git?.push.state === "pushed") toast.dismiss(`push-failed-${a.id}`)
        outcome(a)
      },
      onError: (err) =>
        toast.error("Could not push again", { description: errorMessage(err) }),
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
 * The full review of a git approval, filling its parent. A push's diff is its one scroll
 * frame: the head and the push's facts scroll in above the first file, a bar picks one of its
 * commits, and while it is pending a Comments tab holds the note and line comments. A
 * proposed pull request without a diff shows its text as a page. While pending, the decision
 * stays in view below.
 */
export function GitReview({
  approval,
  headId,
}: {
  approval: Approval
  /** The heading's id, for a region it names. */
  headId?: string
}) {
  const head = <GitReviewHead approval={approval} id={headId} />
  if (approval.kind === "git.review")
    return <ForgeReview approval={approval} head={head} />
  if (approval.kind === "git.comment")
    return (
      <ReviewScroll>
        {head}
        <ForgeComment approval={approval} />
      </ReviewScroll>
    )
  return <PushReview approval={approval} head={head} />
}

/** What a push review keeps of what the reader wrote, until it is sent. */
type PushDraft = {
  note: string
  comments: LineComment[]
  target: FeedbackTarget
}

function PushReview({
  approval,
  head,
}: {
  approval: Approval
  head: ReactNode
}) {
  const git = approval.git
  const diffView = useRef<DiffViewHandle>(null)
  const review = git?.review
  const proposal = review?.pullRequest
  const pending = approval.status === "pending"
  // What the reader wrote and where they were in the diff outlive the review: another tab, a
  // folded screen or a reload finds them again. Line comments sit on the diff's lines, so a diff
  // that changed keeps only the note and where it goes.
  const saved = useReviewDraft<PushDraft>(
    approval.id,
    () => ({
      note: "",
      comments: [],
      target: git?.push.teammateName ? newChatTarget : "",
    }),
    {
      base: draftBase(review?.diff),
      carry: (d, fresh) => ({ ...fresh, note: d.note, target: d.target }),
      off: !pending,
    }
  )
  const { note, comments, target } = saved.draft
  // The note opens under the decision when asked, and by itself when it has text.
  const [noteOpen, setNoteOpen] = useState<boolean>()
  const setNote = (v: string) => saved.change({ note: v })
  const setComments = (v: LineComment[]) => saved.change({ comments: v })
  const setTarget = (v: FeedbackTarget) => saved.change({ target: v })
  // A new branch opens its pull request by default; a push to a branch that has been
  // pushed before but has none doesn't.
  const [openPR, setOpenPR] = useState(!!review?.pullRequest && !review.old)
  const [prTitle, setPRTitle] = useState(proposal?.title ?? "")
  // The line naming the teammate ends the pull request's description only when ticked: the
  // pull request goes up under the user's name.
  const who = git?.push.teammateName ?? ""
  const [attribute, setAttribute] = useState(
    () => !!who && rememberedAttribution(who, "push")
  )
  // A push to the branch a local working copy has checked out updates its files too, as
  // git's updateInstead does; git still refuses it when the copy has changes.
  const [updateCheckedOut, setUpdateCheckedOut] = useState(true)
  // A commit picked in the bar narrows the diff to it; none shows the whole push.
  const [commit, setCommit] = useState("")
  const [progress, setProgress] = useState<ViewedProgress>()
  const decide = useDecideGit()
  const openLater = useOpenPullRequest()
  const pushAgain = usePushAgain()
  const feedback = useSendFeedback()
  if (!git) return null
  const params = {
    params: { path: { env: approval.environmentId, id: approval.id } },
  }
  const lines = parseDiff(git.review?.diff ?? "")
  const fullNote = () => composeNote(note, lines, comments)
  // With no chat or terminal chosen, the comments stay on the push for its agent to read,
  // and the draft clears: the push shows them as left.
  const send = () =>
    feedback.mutate(
      { ...params, body: feedbackBody(target, fullNote()) },
      {
        onSuccess: (out) => {
          if (!out.left) return
          saved.change({ note: "", comments: [] })
        },
      }
    )
  const busy = (action: Action) =>
    decide.isPending && decide.variables.body.action === action
  // A new chat starts only when there is something to tell it beyond the outcome.
  const sends = () => !!target && (target !== newChatTarget || !!fullNote())
  // The note goes to the chat once the decision is in, even if the review closed meanwhile, as
  // when the push left the list: a mutate's own onSuccess doesn't run once it unmounted.
  const act = (action: Action) =>
    decide
      .mutateAsync({
        ...params,
        body: {
          action,
          // Scope only matters for network approvals.
          scope: "sandbox",
          note: fullNote() || undefined,
          pullRequest:
            proposal && action === "allow"
              ? {
                  open: openPR,
                  title: openPR ? prTitle.trim() : undefined,
                  attribution: openPR && attribute ? true : undefined,
                }
              : pr && action === "allow" && attribute
                ? { open: true, attribution: true }
                : undefined,
          force: forced && action === "allow" ? true : undefined,
          updateCheckedOut:
            rv?.checkedOut && action === "allow" ? updateCheckedOut : undefined,
        },
      })
      .then(
        () => {
          saved.drop()
          if (sends()) send()
        },
        () => {} // the hook says what failed
      )
  const rv = git.review
  const pr = git.pullRequest
  const attribution = (pr ?? proposal)?.attribution
  const attributionField = attribution && (
    <label className="flex items-center gap-2 text-xs">
      <Checkbox
        checked={attribute}
        onCheckedChange={(on) => {
          setAttribute(on)
          if (who) rememberAttribution(who, on, "push")
        }}
      />
      <span className="min-w-0 break-words">
        End the description with “{attribution}”
        <span className="text-muted-foreground">
          {" "}
          · off by default, remembered per teammate
        </span>
      </span>
    </label>
  )
  const forced = !pr && !!rv?.forced
  const branch = pr ? `${pr.head} → ${pr.base}` : rv?.branch
  const where = (c: LineComment) => commentWhere(lines, c)
  const jump = (i: number) => {
    setCommit("")
    // The whole push shows again on the next frame.
    requestAnimationFrame(() => diffView.current?.scrollToLine(i))
  }

  const origin = (
    <>
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
    </>
  )

  // Comments left on the push earlier, which the agent reads until a decision replaces them.
  const left = pending && !pr ? git.push.note : undefined
  const leftField = left && (
    <section
      aria-label="Comments left for the agent"
      className="grid max-h-32 gap-1 overflow-auto rounded-md bg-muted/50 px-2 py-1.5 text-xs"
    >
      <p className="text-muted-foreground">
        Left for the agent · new comments replace these, and deciding without
        any sends them along
      </p>
      <p className="break-words whitespace-pre-wrap">{left}</p>
    </section>
  )
  const noteField = (
    <div className="grid gap-2">
      <textarea
        aria-label="Note for the agent"
        placeholder={
          rv
            ? "Note for the agent. Comment on a line of the diff with its button."
            : "Note for the agent"
        }
        rows={rv ? 3 : 2}
        maxLength={4000}
        value={note}
        // Opened with Add a note, it takes the keys.
        autoFocus={!!rv && noteOpen}
        onChange={(e) => setNote(e.target.value)}
        className={textareaClass}
      />
      <p className="text-xs text-muted-foreground">
        {target === newChatTarget
          ? "With a note or line comments, deciding starts a chat in the sandbox that tells the agent the outcome and your comments."
          : target
            ? "Deciding sends the outcome, the note and your line comments there."
            : pr
              ? "The agent sees a rejection and its note when it next fetches or gets a prompt."
              : "The agent reads the outcome and your comments when it checks on the push or gets its next prompt."}
      </p>
    </div>
  )

  const facts = (
    <>
      {head}
      <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-xs">
        {origin}
        <dt className="text-muted-foreground">Branch</dt>
        <dd>
          {branch && <Ident>{branch}</Ident>}
          {rv && !rv.old && (
            <Badge variant="outline" className="ml-1.5 font-sans">
              new branch
            </Badge>
          )}
          {forced && (
            <Badge variant="destructive" className="ml-1.5 font-sans">
              rewrites history
            </Badge>
          )}
        </dd>
        {rv?.base && (forced || !rv.old) && (
          <>
            <dt className="text-muted-foreground">Builds on</dt>
            <dd>
              {rv.baseBranch && <>{rv.baseBranch}&rsquo;s tip </>}
              <Ident>{short(rv.base)}</Ident>
            </dd>
          </>
        )}
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

      {!pending && git.push.state === "failed" && !git.pullRequest && (
        <div
          role="alert"
          className="grid gap-2 rounded-md border border-destructive/40 bg-destructive/5 px-3 py-2.5 text-xs"
        >
          <p className="flex items-center gap-1.5 font-medium text-destructive">
            <WarningIcon className="size-3.5 shrink-0" />
            The push was approved, but it didn&rsquo;t go upstream.
          </p>
          {git.push.result && (
            <p className="break-words text-muted-foreground">
              {git.push.result}
            </p>
          )}
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-muted-foreground">
              The commit is still staged. Once the cause is fixed, for example
              the forge&rsquo;s token in Settings → Git forges, push it again.
            </span>
            <Button
              variant="outline"
              size="xs"
              className="ml-auto"
              disabled={pushAgain.isPending}
              onClick={() => pushAgain.mutate({ ...params })}
            >
              {pushAgain.isPending && <Spinner className="size-3" />}
              Push again
            </Button>
          </div>
        </div>
      )}

      {!pending && canOpenLater(git) && (
        <div className="grid gap-2">
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
              onClick={() =>
                openLater.mutate({
                  ...params,
                  body: { attribution: attribute || undefined },
                })
              }
            >
              {openLater.isPending && <Spinner className="size-3" />}
              Open pull request
            </Button>
          </div>
          {attributionField}
        </div>
      )}

      {rv?.forced && !pr && <Rewrite review={rv} />}
    </>
  )

  const picked = commit ? rv?.commits?.find((c) => c.sha === commit) : undefined
  const showNote = noteOpen ?? !!note.trim()
  const unviewed = progress ? progress.total - progress.viewed : 0

  // The decision, below the review: it stays in view as the review scrolls.
  const decision = (sticky: boolean) =>
    pending && (
      <DecisionRow
        className={cn(
          "grid gap-2 rounded-lg border bg-background/95 p-2",
          sticky && "sticky bottom-0 z-10 shadow-md backdrop-blur"
        )}
      >
        {rv?.checkedOut && (
          <div className="flex flex-wrap items-center gap-2 text-xs">
            <label className="flex items-center gap-2">
              <Checkbox
                checked={updateCheckedOut}
                onCheckedChange={setUpdateCheckedOut}
              />
              Update the checked-out files too
            </label>
            <span className="text-muted-foreground">
              {updateCheckedOut
                ? `${rv.branch} is checked out in the repository; git refuses if it has changes of its own.`
                : `${rv.branch} is checked out in the repository, so git refuses the push.`}
            </span>
          </div>
        )}
        {proposal && (
          <div className="flex flex-wrap items-center gap-2 text-xs">
            <label className="flex items-center gap-2">
              <Checkbox checked={openPR} onCheckedChange={setOpenPR} />
              Also open a pull request into <Ident>{proposal.base}</Ident>
            </label>
            {openPR ? (
              <Input
                aria-label="Pull request title"
                value={prTitle}
                maxLength={200}
                onChange={(e) => setPRTitle(e.target.value)}
                className="h-7 min-w-48 flex-1 text-xs"
              />
            ) : (
              <span className="text-muted-foreground">
                Approving only pushes; you can open the pull request later.
              </span>
            )}
          </div>
        )}
        {(pr || (proposal && openPR)) && attributionField}
        {rv && leftField}
        {rv && showNote && noteField}
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5">
          <div className="min-w-0 flex-1 basis-56 empty:hidden">
            {approval.sandboxId && (
              <FeedbackTargetSelect
                env={approval.environmentId}
                sandboxId={approval.sandboxId}
                newChat={!!git.push.teammateName}
                value={target}
                onChange={setTarget}
              />
            )}
          </div>
          {rv && (
            <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
              <span className="flex items-center gap-1.5 whitespace-nowrap">
                {comments.length
                  ? plural(comments.length, "comment")
                  : "No comments"}{" "}
                · {note.trim() ? "a note" : "no note"}
                <Button
                  size="xs"
                  variant="outline"
                  aria-expanded={showNote}
                  onClick={() => setNoteOpen(!showNote)}
                >
                  {showNote
                    ? "Hide the note"
                    : note.trim()
                      ? "Edit the note"
                      : "Add a note"}
                </Button>
              </span>
              {!picked && unviewed > 0 && (
                <span className="whitespace-nowrap">
                  {plural(unviewed, "file")} not viewed
                </span>
              )}
            </div>
          )}
          <div className="ml-auto flex items-center gap-1.5">
            <DropdownMenu>
              <DropdownMenuTrigger
                render={
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    aria-label="More decisions"
                  />
                }
              >
                <DotsThreeIcon weight="bold" />
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="w-64">
                {target ? (
                  <DropdownMenuItem
                    disabled={feedback.isPending || !fullNote()}
                    onClick={send}
                  >
                    <PaperPlaneRightIcon />
                    <span className="grid">
                      Send without deciding
                      <span className="text-caption text-muted-foreground">
                        The note and comments; the push keeps waiting
                      </span>
                    </span>
                  </DropdownMenuItem>
                ) : (
                  !pr && (
                    <DropdownMenuItem
                      disabled={feedback.isPending || !fullNote()}
                      onClick={send}
                    >
                      <ChatCircleTextIcon />
                      <span className="grid">
                        Leave without deciding
                        <span className="text-caption text-muted-foreground">
                          For the agent to read; the push keeps waiting
                        </span>
                      </span>
                    </DropdownMenuItem>
                  )
                )}
                <DropdownMenuItem
                  disabled={decide.isPending}
                  onClick={() => act("dismiss")}
                >
                  <EyeSlashIcon />
                  <span className="grid">
                    Ignore
                    <span className="text-caption text-muted-foreground">
                      Close it without an answer to the agent
                    </span>
                  </span>
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
            <Button
              variant="outline"
              size="sm"
              disabled={decide.isPending}
              onClick={() => act("deny")}
            >
              {busy("deny") && <Spinner />}
              Reject
            </Button>
            <Button
              size="sm"
              variant={forced ? "destructive" : "default"}
              disabled={
                decide.isPending || (!!proposal && openPR && !prTitle.trim())
              }
              onClick={() => act("allow")}
            >
              {busy("allow") && <Spinner />}
              {pr
                ? "Open pull request"
                : forced
                  ? proposal && openPR
                    ? "Force-push and open pull request"
                    : "Force-push"
                  : proposal && openPR
                    ? "Push and open pull request"
                    : "Approve and push"}
            </Button>
          </div>
        </div>
      </DecisionRow>
    )

  if (!rv)
    return (
      <ReviewScroll>
        {facts}
        {leftField}
        {pending && noteField}
        {decision(true)}
      </ReviewScroll>
    )

  return (
    <div className={reviewPaneClass}>
      <div className="flex min-w-0 flex-wrap items-center gap-2">
        <CommitPicker
          commits={rv.commits ?? []}
          more={rv.moreCommits}
          value={picked?.sha ?? ""}
          onChange={setCommit}
        />
        {rv.diffTruncated && !picked && (
          <Badge variant="outline">diff truncated</Badge>
        )}
        <span className="ml-auto flex flex-wrap items-center justify-end gap-2">
          {!picked && (
            <>
              <ViewedCount
                progress={progress}
                onNext={() => diffView.current?.nextUnviewed()}
              />
              <CommentNav
                entries={[...comments]
                  .sort((a, b) => a.index - b.index)
                  .map((c) => ({
                    key: String(c.index),
                    where: where(c),
                    text: c.text,
                    onPick: () => jump(c.index),
                  }))}
                onStep={(dir) => diffView.current?.nextComment(dir)}
              />
            </>
          )}
          <DiffStyleSwitch />
        </span>
      </div>
      {/* Kept while a commit shows, so the place and the comments being written stay. */}
      <div
        className={cn(
          "flex min-h-0 min-w-0 flex-1 flex-col",
          picked && "hidden"
        )}
      >
        <UnifiedDiff
          diff={rv.diff}
          comments={comments}
          onComment={pending ? setComments : undefined}
          fill
          header={facts}
          place={saved.place}
          onPlace={saved.onPlace}
          viewed={viewedScope(
            git.push.forgeName || git.push.forgeId,
            git.push.owner,
            git.push.repo,
            git.push.ref
          )}
          onViewed={setProgress}
          keys
          ref={diffView}
        />
      </div>
      {picked && (
        <CommitDiff
          key={picked.sha}
          env={approval.environmentId}
          approvalId={approval.id}
          commit={picked}
          pending={pending}
          onAll={() => setCommit("")}
        />
      )}
      {decision(false)}
      {!picked && (
        <ReviewKeys
          comments={pending}
          onHelp={() => diffView.current?.showKeys()}
        />
      )}
    </div>
  )
}

/** What a forced push drops from the forge's branch, and the lease it is pushed with. */
function Rewrite({ review: rv }: { review: NonNullable<GitDetail["review"]> }) {
  const dropped = (rv.dropped ?? []).length + (rv.moreDropped ?? 0)
  const files = rv.tipFiles ?? []
  return (
    <section
      aria-label="Rewrites history"
      className="grid gap-2 rounded-md p-2 text-xs ring-1 ring-destructive/30"
    >
      <p className="flex items-center gap-1.5 font-medium text-destructive">
        <WarningIcon className="size-3.5" />
        Rewrites history · drops {dropped}{" "}
        {dropped === 1 ? "commit" : "commits"} now on the forge
      </p>
      <ul className="min-w-0 divide-y rounded-md ring-1 ring-foreground/10">
        {(rv.dropped ?? []).map((c) => (
          <li
            key={c.sha}
            className="flex min-w-0 items-baseline gap-2 px-2 py-1.5"
          >
            <span aria-hidden className="text-destructive">
              −
            </span>
            <Ident copy value={c.sha} className="text-muted-foreground">
              {short(c.sha)}
            </Ident>
            <span className="truncate text-muted-foreground line-through decoration-muted-foreground/40">
              {c.subject}
            </span>
            {c.replacedBy && (
              <span className="shrink-0 text-muted-foreground">
                → <Ident>{short(c.replacedBy)}</Ident>
              </span>
            )}
            <span className="ml-auto shrink-0 text-muted-foreground">
              {c.author} · {formatAge(c.when)}
            </span>
          </li>
        ))}
        {(rv.moreDropped ?? 0) > 0 && (
          <li className="px-2 py-1.5 text-muted-foreground">
            and {rv.moreDropped} more
          </li>
        )}
      </ul>
      <p className="text-muted-foreground">
        {files.length === 0 ? (
          "The new tip has the same files as the one it replaces: the rewrite only moves or regroups the changes."
        ) : (
          <>
            The old and the new tip differ in{" "}
            {files.map((f, i) => (
              <span key={f}>
                {i > 0 && ", "}
                <Ident>{f}</Ident>
              </span>
            ))}
            {(rv.moreTipFiles ?? 0) > 0 && <> and {rv.moreTipFiles} more</>}.
          </>
        )}
      </p>
      <p className="text-muted-foreground">
        Studio pushes with a lease on <Ident>{short(rv.old)}</Ident>: if the
        branch moved on the forge since, the push is refused and nothing is
        lost. The default branch takes only fast-forward pushes.
      </p>
    </section>
  )
}

/** What reviewing a git approval decides, under its title. */
export function gitReviewDescription(approval: Approval) {
  if (approval.kind === "git.review") return forgeReviewDescription(approval)
  if (approval.kind === "git.comment") return forgeCommentDescription(approval)
  const git = approval.git
  if (git?.pullRequest)
    return "The push was approved and is on the forge. Approving opens this pull request."
  const upstream = git?.review?.upstream
  if (git?.review?.forced)
    return (
      `The sandbox rewrote ${git.review.branch} and pushed it to Studio's git remote. Nothing reaches the forge until you force-push it, which drops the commits listed under Rewrites history` +
      (upstream ? `; Studio pushes it ${upstream}` : "") +
      "."
    )
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

/** What a decided push or pull request came to, in one sentence. */
const decided: Record<string, string> = {
  approved: "This was approved.",
  denied: "This was rejected.",
  dismissed: "This was ignored.",
}

/**
 * A git review's heading: what it asks and when, and while it waits what deciding it does; once
 * decided, what it came to.
 */
export function GitReviewHead({
  approval: a,
  id,
}: {
  approval: Approval
  /** The heading's id, for a region it names. */
  id?: string
}) {
  const kind = approvalKind(a)
  return (
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
        <h2 id={id} className="text-sm font-medium break-words">
          {gitSummary(a)}
        </h2>
        {a.status === "pending" && (
          <p className="text-muted-foreground">{gitReviewDescription(a)}</p>
        )}
      </div>
      {a.status !== "pending" && (
        <p
          role="status"
          className="rounded-md border border-dashed px-3 py-2.5 font-medium"
        >
          {a.git?.push.state === "failed" && !a.git.pullRequest
            ? "This was approved, but the push failed; why is below."
            : `${decided[a.status] ?? `This is ${a.status}.`} Nothing is left to decide; the outcome is below.`}
        </p>
      )}
    </>
  )
}

/**
 * A push waiting for its review opens with the teammate's screen enlarged over the
 * conversation, so the diff has the width; Esc or the folded conversation brings the chat back.
 * The rest open beside it.
 */
export const opensEnlarged = (a: Approval) =>
  a.kind === "git.push" && a.status === "pending"

/**
 * Opens a git approval's review where it is decided: its computer's Changes tab, beside the
 * thread in view when the computer is the open teammate's (over it, for a waiting push). A
 * review whose computer is gone opens its own page.
 */
export function useOpenGitReview() {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const teammatePage = useMatch({
    from: "/e/$env/teammates/$id",
    shouldThrow: false,
  })
  return async (a: Approval) => {
    const env = a.environmentId
    const list = a.sandboxId
      ? await queryClient
          .ensureQueryData(
            $api.queryOptions("get", "/api/environments/{env}/sandboxes", {
              params: { path: { env } },
            })
          )
          .catch(() => undefined)
      : undefined
    const sb = list?.find((s) => s.id === a.sandboxId)
    if (!sb)
      return navigate({
        to: "/e/$env/git/reviews/$id",
        params: { env, id: a.id },
      })
    if (
      sb.teammateId &&
      teammatePage?.params.env === env &&
      teammatePage.params.id === sb.teammateId
    )
      return navigate({
        to: "/e/$env/teammates/$id",
        params: { env, id: sb.teammateId },
        search: (prev) => ({
          ...prev,
          tab: "changes" as const,
          review: a.id,
          change: undefined,
          computer: sb.id,
          fold: undefined,
          full: opensEnlarged(a) || undefined,
        }),
      })
    // A teammate's computer redirects to the teammate's desk, enlarged there for a waiting
    // push; a computer of its own has the page already.
    return navigate({
      to: "/e/$env/sandboxes/$id",
      params: { env, id: sb.id },
      search: {
        tab: "changes",
        review: a.id,
        ...(sb.teammateId && opensEnlarged(a) ? { full: true } : {}),
      },
    })
  }
}

/** A compact card body for the approval stack: what is asked, and a button to review it. */
export function GitApprovalSummary({
  approval,
  onReview,
}: {
  approval: Approval
  onReview: () => void
}) {
  if (approval.kind === "git.review")
    return <ForgeReviewSummary approval={approval} onReview={onReview} />
  if (approval.kind === "git.comment")
    return <ForgeCommentSummary approval={approval} onReview={onReview} />
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
