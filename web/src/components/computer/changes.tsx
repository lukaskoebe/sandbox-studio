import {
  type CSSProperties,
  type ReactNode,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react"
import { useQueryClient } from "@tanstack/react-query"
import {
  ArrowLeftIcon,
  BellIcon,
  CaretRightIcon,
  ChatCircleIcon,
  CheckCircleIcon,
  CircleDashedIcon,
  GitBranchIcon,
  GitPullRequestIcon,
  XCircleIcon,
} from "@phosphor-icons/react"
import { ChangesPanel } from "@/components/chat/changes-panel"
import { requestLabel } from "@/components/chat/chat-thread"
import { FilesPanel } from "@/components/chat/files-panel"
import {
  ChangeView,
  firstChange,
  GitRail,
  gitChangedPaths,
  RailRow,
  type useGitStatus,
} from "@/components/computer/git-changes"
import {
  RailSection,
  useRailFolds,
  useSectionKeys,
} from "@/components/computer/rail-sections"
import { ErrorBoundary } from "@/components/error-boundary"
import {
  ResizeHandle,
  railSplit,
  useElementWidth,
  useStoredSize,
} from "@/components/resize-handle"
import { GitReview, GitReviewHead } from "@/components/git-review"
import { pullRequestName } from "@/lib/approval-kinds"
import { Button } from "@/components/ui/button"
import { ScrollArea } from "@/components/ui/scroll-area"
import { useClaimApprovals } from "@/lib/approval-claims"
import { cn } from "@/lib/utils"
import { $api, type Approval, type Sandbox } from "@/lib/api/client"
import {
  changeCount,
  type Chat,
  type ChatItem,
  type ChatTurn,
} from "@/lib/chats"

export type ScreenThread = {
  chat: Chat
  turns: ChatTurn[]
  items: ChatItem[]
  turnId?: string
  onTurn: (id: string) => void
}

/** The turns of a thread that changed files, newest first. */
function useChangedTurns(thread?: ScreenThread) {
  return useMemo(
    () =>
      (thread?.turns ?? [])
        .filter((t) => t.endedAt && changeCount(t) > 0)
        .reverse(),
    [thread?.turns]
  )
}

const gitPaths = [
  "/api/environments/{env}/sandboxes/{id}/git",
  "/api/environments/{env}/sandboxes/{id}/git/diff",
]
const filesPaths = [
  "/api/environments/{env}/sandboxes/{id}/files",
  "/api/environments/{env}/sandboxes/{id}/files/content",
]

/** Files may change whenever a request ends: the tab asks again then. */
function useRefreshOnTurnEnd(
  thread: ScreenThread | undefined,
  paths: string[]
) {
  const queryClient = useQueryClient()
  const ended = (thread?.turns ?? []).filter((t) => t.endedAt).length
  useEffect(() => {
    for (const path of paths)
      void queryClient.invalidateQueries({ queryKey: ["get", path] })
  }, [ended, paths, queryClient])
}

/** What the Changes tab shows beside its rail: a push's review, a change, or the thread's. */
export type ChangesPick = { review?: string; change?: string }

/** How long an allowed push stays in the Pushed section. */
const pushedFor = 24 * 60 * 60 * 1000

/** This computer's pushes and pull requests allowed in the last day, newest first. */
function usePushed(env: string, sandboxId: string) {
  const approved = $api.useQuery(
    "get",
    "/api/environments/{env}/approvals",
    { params: { path: { env }, query: { status: "approved" } } },
    { refetchInterval: 15_000 }
  )
  return useMemo(() => {
    const since = Date.now() - pushedFor
    return (approved.data ?? [])
      .filter(
        (a) =>
          a.sandboxId === sandboxId &&
          (a.kind === "git.push" || a.kind === "git.pr") &&
          Date.parse(a.updatedAt) >= since
      )
      .sort((a, b) => Date.parse(b.updatedAt) - Date.parse(a.updatedAt))
  }, [approved.data, sandboxId])
}

/** A waiting review's row: a push by its branch, a forge review or comment by its pull request. */
function waitingRow(a: Approval) {
  const c = a.forgeComment
  if (c) {
    const pr = `${c.owner}/${c.repo}#${c.number}`
    return {
      label: `Review the comment on ${pr}`,
      what: "Comment on",
      mono: pr,
    }
  }
  const d = a.forgeReview?.draft
  if (d) {
    const pr = pullRequestName(d)
    return { label: `Review the review of ${pr}`, what: "Review of", mono: pr }
  }
  const branch = a.git?.review?.branch ?? a.git?.pullRequest?.head ?? "push"
  return {
    label: `Review the push to ${branch}`,
    what: "Push to",
    mono: branch,
  }
}

/** A pushed row's words: where it went, and what became of it. */
function pushedLine(a: Approval) {
  const push = a.git?.push
  const branch = (push?.ref ?? "").replace(/^refs\/heads\//, "") || "push"
  const pr = push?.prUrl?.match(/(\d+)\/?$/)?.[1]
  if (a.kind === "git.pr")
    return {
      what: `Pull request from ${a.git?.pullRequest?.head ?? branch}`,
      state: pr ? `#${pr}` : "opened",
      failed: false,
    }
  const failed = push?.state === "failed"
  return {
    what: `Push to ${branch}`,
    state: failed ? "failed" : pr ? `#${pr}` : "pushed",
    failed,
  }
}

const prStates = {
  open: { label: "open", className: "text-(--st-running)" },
  merged: { label: "merged", className: "text-(--acc-git)" },
  closed: { label: "closed", className: "text-muted-foreground" },
} as const

const checkStates = {
  pending: {
    icon: CircleDashedIcon,
    label: "checks running",
    className: "text-(--st-waiting)",
  },
  success: {
    icon: CheckCircleIcon,
    label: "checks passed",
    className: "text-(--st-running)",
  },
  failure: {
    icon: XCircleIcon,
    label: "checks failed",
    className: "text-(--st-failed)",
  },
} as const

/**
 * A row of the Pushed section: where the push went and, once its forge says, its pull request's
 * number and state, the sum of its checks and its comments. The forge is asked once a minute.
 */
function PushedRow({
  env,
  approval: a,
  current,
  onClick,
}: {
  env: string
  approval: Approval
  current: boolean
  onClick: () => void
}) {
  const line = pushedLine(a)
  const pr = $api.useQuery(
    "get",
    "/api/environments/{env}/approvals/{id}/pull-request",
    { params: { path: { env, id: a.id } } },
    { enabled: !line.failed, refetchInterval: 60_000, retry: false }
  ).data
  const state = pr?.found && pr.state ? prStates[pr.state] : undefined
  const checks =
    pr?.found && pr.checks && pr.checks !== "none"
      ? checkStates[pr.checks]
      : undefined
  return (
    <RailRow current={current} onClick={onClick}>
      <GitPullRequestIcon className="size-3.5 shrink-0 text-(--acc-git)" />
      <span className="min-w-0 flex-1 truncate">{line.what}</span>
      {pr?.found ? (
        <span className="flex shrink-0 items-center gap-1.5 font-mono text-meta text-muted-foreground">
          <span>#{pr.number}</span>
          {state && <span className={state.className}>{state.label}</span>}
          {checks && (
            <span
              className={cn("inline-flex", checks.className)}
              title={checks.label}
            >
              <checks.icon aria-hidden className="size-3.5" />
              <span className="sr-only">{checks.label}</span>
            </span>
          )}
          {pr.comments > 0 && (
            <span
              className="inline-flex items-center gap-0.5"
              title={`${pr.comments} ${pr.comments === 1 ? "comment" : "comments"}`}
            >
              <ChatCircleIcon aria-hidden className="size-3.5" />
              {pr.comments}
              <span className="sr-only">
                {pr.comments === 1 ? " comment" : " comments"}
              </span>
            </span>
          )}
        </span>
      ) : (
        <span
          className={cn(
            "shrink-0 font-mono text-meta",
            line.failed ? "text-destructive" : "text-muted-foreground"
          )}
        >
          {line.state}
        </span>
      )}
    </RailRow>
  )
}

type SectionId = "waiting" | "pushed" | "repositories" | "thread"

/**
 * The screen's Changes tab, git's view of the computer (mock A): a rail with the pushes that
 * wait for review, each repository of /workspace with its branch and what is staged, not
 * staged and untracked, and what the open thread's requests changed; the one picked shows
 * beside it. The pick is in the URL; with none, the first change shows. On a narrow screen
 * the rail comes first and a pick opens over it.
 */
export function ChangesView({
  env,
  sandbox: sb,
  reviews,
  review,
  change,
  onPick,
  git,
  onOpenFile,
  thread,
}: {
  env: string
  sandbox: Sandbox
  /** The pushes from this computer that wait for review. */
  reviews: Approval[]
  /** The review a link names. */
  review?: string
  /** The change a link names: a changeKey, or "thread" for the thread's changes. */
  change?: string
  onPick: (pick: ChangesPick) => void
  git: ReturnType<typeof useGitStatus>
  /** Shows a file in the Files tab, relative to /workspace. */
  onOpenFile: (path: string) => void
  thread?: ScreenThread
}) {
  const changedTurns = useChangedTurns(thread)
  const pushed = usePushed(env, sb.id)
  const approval =
    reviews.find((a) => a.id === review) ?? pushed.find((a) => a.id === review)
  // The tab decides the review in view, so the floating stack leaves its card out.
  useClaimApprovals(approval?.status === "pending" ? [approval.id] : [])
  useRefreshOnTurnEnd(thread, gitPaths)
  const turn =
    changedTurns.find((t) => t.id === thread?.turnId) ?? changedTurns.at(0)
  const picked = approval ? `review:${approval.id}` : change
  const first =
    firstChange(git.data?.repos) ??
    (thread && changedTurns.length > 0 ? "thread" : undefined)
  const shown = picked ?? first
  const files = new Set(
    changedTurns.flatMap((t) => (t.changes?.files ?? []).map((f) => f.path))
  ).size

  const back = (
    <Button
      variant="ghost"
      size="sm"
      className="m-2 mb-0 justify-self-start text-muted-foreground @2xl:hidden"
      onClick={() => pick({})}
    >
      <ArrowLeftIcon />
      Changes
    </Button>
  )
  let main: ReactNode = (
    <p className="p-4 text-xs text-muted-foreground">
      Nothing has changed on this computer.
    </p>
  )
  if (approval)
    main = (
      <ScrollArea className="min-h-0 flex-1" viewportClassName="p-3 md:p-4">
        <section
          aria-labelledby={`review-${approval.id}`}
          data-testid="git-review"
          className="grid gap-3 text-xs"
        >
          <GitReviewHead approval={approval} id={`review-${approval.id}`} />
          <GitReview key={approval.id} approval={approval} />
        </section>
      </ScrollArea>
    )
  else if (shown === "thread" && thread)
    main = (
      <ScrollArea className="min-h-0 flex-1" viewportClassName="p-3 md:p-4">
        <section aria-label="This thread's changes" className="text-xs">
          {changedTurns.length > 0 ? (
            <ChangesPanel
              env={env}
              chatId={thread.chat.id}
              turns={changedTurns}
              turnId={turn?.id}
              onTurnChange={thread.onTurn}
              label={(t) => requestLabel(thread.items, t)}
            />
          ) : (
            <p className="text-muted-foreground">
              Its requests haven't changed any files yet.
            </p>
          )}
        </section>
      </ScrollArea>
    )
  else if (shown && shown !== "thread")
    main = (
      <ChangeView
        env={env}
        sandboxId={sb.id}
        status={git}
        open={shown}
        onOpenFile={onOpenFile}
      />
    )

  const folds = useRailFolds(sb.id)
  // A review link, as from a notification, opens its section and folds the others until the
  // user asks for all of them or leaves the tab. A pick in the rail is not a link.
  const pickedHere = useRef<string | undefined>(undefined)
  type Visit = { opened: Set<SectionId>; closed: Set<SectionId> }
  const visitFor = (id?: string): Visit | undefined =>
    id ? { opened: new Set(), closed: new Set() } : undefined
  // The link's section: a push still waiting, or one already decided.
  const mark: SectionId =
    approval && approval.status !== "pending" ? "pushed" : "waiting"
  const [visit, setVisit] = useState(() => visitFor(review))
  const [lastReview, setLastReview] = useState(review)
  if (review !== lastReview) {
    setLastReview(review)
    if (review && review !== pickedHere.current) setVisit(visitFor(review))
  }
  const pick = (p: ChangesPick) => {
    pickedHere.current = p.review
    onPick(p)
  }
  const fold = (id: SectionId) => ({
    open: visit
      ? !visit.closed.has(id) && (id === mark || visit.opened.has(id))
      : folds.section(id),
    onOpenChange: (open: boolean) => {
      if (!visit) return folds.setSection(id, open)
      const opened = new Set(visit.opened)
      const closed = new Set(visit.closed)
      if (open) {
        opened.add(id)
        closed.delete(id)
      } else {
        opened.delete(id)
        closed.add(id)
      }
      setVisit({ opened, closed })
    },
    marked: !!visit && id === mark,
  })
  const railRef = useRef<HTMLDivElement>(null)
  useSectionKeys(railRef)
  const repos = git.data?.repos ?? []
  const changed = repos.reduce((n, r) => n + (r.changes?.length ?? 0), 0)
  const repoCount =
    repos.length === 0
      ? undefined
      : `${repos.length} · ${changed} ${changed === 1 ? "file" : "files"}`

  const [box, boxWidth] = useElementWidth<HTMLDivElement>()
  const [rail, setRail] = useStoredSize(railSplit.key, railSplit.default)
  const railMax = Math.max(
    railSplit.min,
    Math.min(railSplit.max, boxWidth - railSplit.contentMin)
  )
  const railWidth = Math.min(Math.max(rail, railSplit.min), railMax)

  return (
    <div ref={box} className="@container flex min-h-0 min-w-0 flex-1">
      <div className="flex min-h-0 min-w-0 flex-1">
        <ScrollArea
          aria-label="Changes"
          className={cn(
            "min-h-0 w-full flex-1 @2xl:w-(--rail) @2xl:flex-none @2xl:border-r",
            picked && "hidden @2xl:block"
          )}
          style={{ "--rail": `${railWidth}px` } as CSSProperties}
        >
          <div ref={railRef} className="grid min-w-0 text-xs">
            {visit && (
              <div
                role="status"
                className="flex items-start gap-2 border-b bg-muted/40 px-3 py-2 text-muted-foreground"
              >
                <BellIcon className="mt-px size-3.5 shrink-0" />
                <span className="min-w-0 flex-1">
                  Opened from a review link. The other sections are folded for
                  this visit.
                </span>
                <button
                  type="button"
                  className="shrink-0 text-foreground underline underline-offset-2 outline-none hover:no-underline focus-visible:ring-2 focus-visible:ring-ring/30"
                  onClick={() => setVisit(undefined)}
                >
                  Show all
                </button>
              </div>
            )}
            {reviews.length > 0 && (
              <RailSection
                label="Waiting for you"
                count={reviews.length}
                {...fold("waiting")}
              >
                <ul className="min-w-0">
                  {reviews.map((a) => {
                    const { label, what, mono } = waitingRow(a)
                    return (
                      <li key={a.id} className="min-w-0">
                        <RailRow
                          current={shown === `review:${a.id}`}
                          aria-label={label}
                          onClick={() => pick({ review: a.id })}
                        >
                          <GitBranchIcon className="size-3.5 shrink-0 text-(--acc-git)" />
                          <span className="min-w-0 flex-1 truncate">
                            {what} <span className="font-mono">{mono}</span>
                          </span>
                          <span className="shrink-0 text-(--st-waiting)">
                            review
                          </span>
                        </RailRow>
                      </li>
                    )
                  })}
                </ul>
              </RailSection>
            )}
            {pushed.length > 0 && (
              <RailSection
                label="Pushed"
                count={pushed.length}
                {...fold("pushed")}
              >
                <ul className="min-w-0">
                  {pushed.map((a) => (
                    <li key={a.id} className="min-w-0">
                      <PushedRow
                        env={env}
                        approval={a}
                        current={shown === `review:${a.id}`}
                        onClick={() => pick({ review: a.id })}
                      />
                    </li>
                  ))}
                </ul>
              </RailSection>
            )}
            <RailSection
              label="Repositories"
              count={repoCount}
              {...fold("repositories")}
            >
              <GitRail
                status={git}
                open={shown}
                idle={!picked}
                onOpen={(key) => pick({ change: key })}
                repoOpen={folds.repo}
                onRepoOpenChange={folds.setRepo}
              />
            </RailSection>
            {thread && (
              <RailSection
                label="This thread"
                count={`${changedTurns.length} ${changedTurns.length === 1 ? "request" : "requests"}`}
                {...fold("thread")}
              >
                <RailRow
                  current={shown === "thread"}
                  idle={!picked}
                  onClick={() => pick({ change: "thread" })}
                >
                  <ChatCircleIcon className="size-3.5 shrink-0 text-(--acc-chat)" />
                  <span className="min-w-0 flex-1 truncate">
                    {changedTurns.length === 0
                      ? "No files changed yet"
                      : `${changedTurns.length} ${changedTurns.length === 1 ? "request" : "requests"} changed ${files} ${files === 1 ? "file" : "files"}`}
                  </span>
                  <CaretRightIcon className="size-3 shrink-0 text-muted-foreground" />
                </RailRow>
              </RailSection>
            )}
          </div>
        </ScrollArea>
        <ResizeHandle
          label="the list of changes"
          side="left"
          size={railWidth}
          min={railSplit.min}
          max={railMax}
          defaultSize={railSplit.default}
          onResize={setRail}
          className="-mx-1 hidden @2xl:block"
        />
        <div
          className={cn(
            "min-h-0 min-w-0 flex-1 flex-col",
            picked ? "flex" : "hidden @2xl:flex"
          )}
        >
          {picked && back}
          <ErrorBoundary label="the change" resetKey={shown}>
            {main}
          </ErrorBoundary>
        </div>
      </div>
    </div>
  )
}

/**
 * The screen's Files tab: the computer's files, the ones git reports changed and the ones the
 * thread's request changed marked.
 */
export function FilesView({
  env,
  sandbox: sb,
  files = "",
  file,
  onFiles,
  git,
  thread,
}: {
  env: string
  sandbox: Sandbox
  /** The folder Files shows, from the URL; "" or undefined for /workspace. */
  files?: string
  /** The file Files shows, from the URL. */
  file?: string
  /** Moves Files to a folder or file: a step in the browser's history. */
  onFiles: (dir: string, file?: string) => void
  git: ReturnType<typeof useGitStatus>
  thread?: ScreenThread
}) {
  const changedTurns = useChangedTurns(thread)
  const turn =
    changedTurns.find((t) => t.id === thread?.turnId) ?? changedTurns.at(0)
  const repos = git.data?.repos
  const changed = useMemo(
    () =>
      new Set([
        ...(turn?.changes?.files ?? []).map((f) => f.path),
        ...gitChangedPaths(repos),
      ]),
    [turn, repos]
  )
  useRefreshOnTurnEnd(thread, filesPaths)
  return (
    <FilesPanel
      env={env}
      sandboxId={sb.id}
      dir={file ? file.split("/").slice(0, -1).join("/") : files}
      file={file}
      onOpen={onFiles}
      changed={changed}
      className="p-3 md:p-4"
    />
  )
}
