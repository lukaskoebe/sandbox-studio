import {
  ForgeComment,
  ForgeCommentSummary,
  forgeCommentDescription,
} from "@/components/forge-comment"
import { useRef, useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { useMatch, useNavigate } from "@tanstack/react-router"
import { toast } from "sonner"
import {
  CaretRightIcon,
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
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Input } from "@/components/ui/input"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Spinner } from "@/components/ui/spinner"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { CountPill } from "@/components/count-pill"
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
import { textareaClass } from "@/components/memory-dialogs"
import {
  FeedbackTargetSelect,
  UnifiedDiff,
  commentWhere,
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
import { draftBase, useReviewDraft } from "@/lib/review-drafts"
import { formatAge } from "@/lib/utils"

type Action = "allow" | "deny" | "dismiss"

type Commit = NonNullable<
  NonNullable<NonNullable<GitDetail["review"]>["commits"]>
>[number]

/** One commit of a push, opening to its own diff. */
function CommitRow({
  commit: c,
  env,
  approvalId,
}: {
  commit: Commit
  env: string
  approvalId: string
}) {
  return (
    <Collapsible render={<li />} className="group/commit grid min-w-0">
      <div className="flex min-w-0 items-baseline gap-2 px-2 py-1.5">
        <Ident copy value={c.sha} className="text-muted-foreground">
          {short(c.sha)}
        </Ident>
        <CollapsibleTrigger
          aria-label={`Changes of ${short(c.sha)}: ${c.subject}`}
          className="flex min-w-0 flex-1 items-baseline gap-2 rounded-sm text-left outline-none hover:underline focus-visible:ring-2 focus-visible:ring-ring/30"
        >
          <span className="truncate font-medium">{c.subject}</span>
          <span className="ml-auto shrink-0 text-muted-foreground">
            {c.author} · {formatAge(c.when)}
          </span>
          <CaretRightIcon className="size-3 shrink-0 self-center text-muted-foreground transition-transform group-data-[open]/commit:rotate-90" />
        </CollapsibleTrigger>
      </div>
      {c.body && (
        <p className="-mt-1 px-2 pb-1.5 break-words whitespace-pre-wrap text-muted-foreground">
          {c.body}
        </p>
      )}
      <CollapsibleContent className="min-w-0 border-t p-2">
        <CommitChanges env={env} approvalId={approvalId} sha={c.sha} />
      </CollapsibleContent>
    </Collapsible>
  )
}

function CommitChanges({
  env,
  approvalId,
  sha,
}: {
  env: string
  approvalId: string
  sha: string
}) {
  const q = $api.useQuery(
    "get",
    "/api/environments/{env}/approvals/{id}/commits/{sha}",
    { params: { path: { env, id: approvalId, sha } } },
    { staleTime: Infinity, retry: false }
  )
  if (q.isPending)
    return <p className="text-muted-foreground">Loading the changes…</p>
  if (q.isError)
    return (
      <p className="text-muted-foreground">
        {errorStatus(q.error) === 404
          ? "Studio no longer keeps this commit."
          : `Could not load the changes: ${errorMessage(q.error)}`}
      </p>
    )
  const d = q.data
  return (
    <div className="grid min-w-0 gap-1.5">
      <p className="flex items-center gap-2 text-muted-foreground">
        {d.files} {d.files === 1 ? "file" : "files"} ·{" "}
        <span className="text-emerald-600 dark:text-emerald-400">
          +{d.additions}
        </span>
        <span className="text-destructive">−{d.deletions}</span>
        {d.diffTruncated && (
          <Badge variant="outline" className="ml-auto">
            diff truncated
          </Badge>
        )}
      </p>
      <UnifiedDiff diff={d.diff} fill />
    </div>
  )
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

type Tab = "changes" | "commits" | "comments"

/**
 * The full review of a git approval. A push has tabs: its diff, its commits with where it
 * comes from, and while it is pending the note and line comments. A proposed pull request
 * shows its text. While pending, a footer that stays in view holds the decision.
 */
export function GitReview({ approval }: { approval: Approval }) {
  if (approval.kind === "git.review") return <ForgeReview approval={approval} />
  if (approval.kind === "git.comment")
    return <ForgeComment approval={approval} />
  return <PushReview approval={approval} />
}

/** What a push review keeps of what the reader wrote, until it is sent. */
type PushDraft = {
  note: string
  comments: LineComment[]
  target: FeedbackTarget
}

function PushReview({ approval }: { approval: Approval }) {
  const git = approval.git
  const diffView = useRef<DiffViewHandle>(null)
  const review = git?.review
  const proposal = review?.pullRequest
  const pending = approval.status === "pending"
  // What the reader wrote, the tab and where they were in the diff outlive the review: another
  // tab, a folded screen or a reload finds them again. Line comments sit on the diff's lines, so
  // a diff that changed keeps only the note and where it goes.
  const saved = useReviewDraft<PushDraft, Tab>(
    approval.id,
    () => ({
      note: "",
      comments: [],
      target: git?.push.teammateName ? newChatTarget : "",
    }),
    {
      base: draftBase(review?.diff),
      carry: (d, fresh) => ({ ...fresh, note: d.note, target: d.target }),
      tab: "changes",
      off: !pending,
    }
  )
  const { note, comments, target } = saved.draft
  const tab = saved.tab ?? "changes"
  const setTab = saved.setTab
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
    setTab("changes")
    // The panel shows on the next frame.
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
  const noteField = (
    <div className="grid gap-2">
      {left && (
        <section
          aria-label="Comments left for the agent"
          className="grid gap-1 rounded-md bg-muted/50 px-2 py-1.5 text-xs"
        >
          <p className="text-muted-foreground">
            Left for the agent · new comments replace these, and deciding
            without any sends them along
          </p>
          <p className="break-words whitespace-pre-wrap">{left}</p>
        </section>
      )}
      <textarea
        aria-label="Note for the agent"
        placeholder={
          rv
            ? "Note for the agent. Comment on a line of the diff with its button."
            : "Note for the agent"
        }
        rows={rv ? 4 : 2}
        maxLength={4000}
        value={note}
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

  return (
    <div className="grid min-w-0 gap-4">
      <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-xs">
        {!rv && origin}
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

      {rv && (
        <Tabs
          value={tab}
          onValueChange={(v: Tab) => setTab(v)}
          className="min-w-0"
        >
          <div className="flex min-w-0 items-center gap-2">
            <TabsList>
              <TabsTrigger value="changes">Changes</TabsTrigger>
              <TabsTrigger value="commits">
                Commits
                <CountPill count={(rv.commits?.length ?? 0) + rv.moreCommits} />
              </TabsTrigger>
              {pending && (
                <TabsTrigger value="comments">
                  Comments
                  <CountPill
                    count={
                      comments.length + (note.trim() ? 1 : 0) + (left ? 1 : 0)
                    }
                  />
                </TabsTrigger>
              )}
            </TabsList>
            {rv.diffTruncated && (
              <Badge variant="outline" className="ml-auto">
                diff truncated
              </Badge>
            )}
          </div>
          <TabsContent value="changes" keepMounted className="min-w-0">
            <UnifiedDiff
              diff={rv.diff}
              comments={comments}
              onComment={pending ? setComments : undefined}
              fill
              place={saved.place}
              onPlace={saved.onPlace}
              ref={diffView}
            />
          </TabsContent>
          <TabsContent
            value="commits"
            keepMounted
            className="grid min-w-0 gap-3"
          >
            <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-xs">
              {origin}
            </dl>
            <ul className="min-w-0 divide-y rounded-md text-xs ring-1 ring-foreground/10">
              {(rv.commits ?? []).map((c) => (
                <CommitRow
                  key={c.sha}
                  commit={c}
                  env={approval.environmentId}
                  approvalId={approval.id}
                />
              ))}
              {rv.moreCommits > 0 && (
                <li className="px-2 py-1.5 text-muted-foreground">
                  and {rv.moreCommits} more
                </li>
              )}
            </ul>
          </TabsContent>
          {pending && (
            <TabsContent value="comments" keepMounted className="grid gap-4">
              {noteField}
              <section className="grid gap-2">
                <SectionHeader
                  title="Line comments"
                  icon={ChatCircleTextIcon}
                  area="git"
                  count={comments.length}
                />
                {comments.length === 0 ? (
                  <p className="text-xs text-muted-foreground">
                    None yet. In Changes, click a line's number to comment on
                    it, or drag over several numbers to comment on them all.
                  </p>
                ) : (
                  <ul
                    aria-label="Line comments"
                    className="divide-y rounded-md text-xs ring-1 ring-foreground/10"
                  >
                    {[...comments]
                      .sort((a, b) => a.index - b.index)
                      .map((c) => (
                        <li key={c.index}>
                          <button
                            type="button"
                            title="Show the line"
                            className="grid w-full gap-0.5 px-2 py-1.5 text-left hover:bg-muted/50"
                            onClick={() => jump(c.index)}
                          >
                            <Ident className="text-muted-foreground">
                              {where(c)}
                            </Ident>
                            <span className="break-words whitespace-pre-wrap">
                              {c.text}
                            </span>
                          </button>
                        </li>
                      ))}
                  </ul>
                )}
              </section>
            </TabsContent>
          )}
        </Tabs>
      )}

      {pending && !rv && noteField}

      {pending && (
        <div
          role="group"
          aria-label="Decision"
          className="sticky bottom-0 z-10 grid gap-2 rounded-lg border bg-background/95 p-2 shadow-md backdrop-blur"
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
          <div className="flex flex-wrap items-center gap-1.5">
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
        </div>
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
 * Opens a git approval's review where it is decided: its computer's Changes tab, beside the
 * thread in view when the computer is the open teammate's. A review whose computer is gone
 * opens its own page.
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
        }),
      })
    // A teammate's computer redirects to the teammate's desk.
    return navigate({
      to: "/e/$env/sandboxes/$id",
      params: { env, id: sb.id },
      search: { tab: "changes", review: a.id },
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
