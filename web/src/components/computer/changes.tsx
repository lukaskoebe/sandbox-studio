import { useEffect, useMemo, type ReactNode } from "react"
import { useQueryClient } from "@tanstack/react-query"
import {
  ArrowLeftIcon,
  CaretRightIcon,
  GitBranchIcon,
} from "@phosphor-icons/react"
import { ChangesPanel } from "@/components/chat/changes-panel"
import { requestLabel } from "@/components/chat/chat-thread"
import { FilesPanel } from "@/components/chat/files-panel"
import {
  ChangeView,
  firstChange,
  GitRail,
  gitChangedPaths,
  RailLabel,
  RailRow,
  type useGitStatus,
} from "@/components/computer/git-changes"
import { ErrorBoundary } from "@/components/error-boundary"
import { GitReview, gitReviewDescription } from "@/components/git-review"
import { Button } from "@/components/ui/button"
import { ScrollArea } from "@/components/ui/scroll-area"
import { useClaimApprovals } from "@/lib/approval-claims"
import { cn } from "@/lib/utils"
import type { Approval, Sandbox } from "@/lib/api/client"
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
  const approval = reviews.find((a) => a.id === review)
  // The tab decides the review in view, so the floating stack leaves its card out.
  useClaimApprovals(approval ? [approval.id] : [])
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
      onClick={() => onPick({})}
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
        <div className="grid gap-3 text-xs">
          <p className="text-muted-foreground">
            {gitReviewDescription(approval)}
          </p>
          <GitReview key={approval.id} approval={approval} />
        </div>
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

  return (
    <div className="@container flex min-h-0 min-w-0 flex-1">
      <div className="flex min-h-0 min-w-0 flex-1">
        <ScrollArea
          aria-label="Changes"
          className={cn(
            "min-h-0 w-full flex-1 @2xl:w-68 @2xl:flex-none @2xl:border-r",
            picked && "hidden @2xl:block"
          )}
        >
          <div className="grid min-w-0 text-xs">
            {reviews.length > 0 && (
              <section
                aria-label="Waiting for you"
                className="min-w-0 border-b pb-1.5"
              >
                <RailLabel level={3}>Waiting for you</RailLabel>
                <ul className="min-w-0">
                  {reviews.map((a) => {
                    const branch =
                      a.git?.review?.branch ??
                      a.git?.pullRequest?.head ??
                      "push"
                    return (
                      <li key={a.id} className="min-w-0">
                        <RailRow
                          current={shown === `review:${a.id}`}
                          aria-label={`Review the push to ${branch}`}
                          onClick={() => onPick({ review: a.id })}
                        >
                          <GitBranchIcon className="size-3.5 shrink-0 text-(--acc-git)" />
                          <span className="min-w-0 flex-1 truncate">
                            Push to <span className="font-mono">{branch}</span>
                          </span>
                          <span className="shrink-0 text-(--st-waiting)">
                            review
                          </span>
                        </RailRow>
                      </li>
                    )
                  })}
                </ul>
              </section>
            )}
            <GitRail
              status={git}
              open={shown}
              idle={!picked}
              onOpen={(key) => onPick({ change: key })}
            />
            {thread && (
              <section aria-label="This thread" className="min-w-0 pb-1.5">
                <RailLabel level={3}>This thread</RailLabel>
                <RailRow
                  current={shown === "thread"}
                  idle={!picked}
                  onClick={() => onPick({ change: "thread" })}
                >
                  <span className="min-w-0 flex-1 truncate">
                    {changedTurns.length === 0
                      ? "No files changed yet"
                      : `${changedTurns.length} ${changedTurns.length === 1 ? "request" : "requests"} changed ${files} ${files === 1 ? "file" : "files"}`}
                  </span>
                  <CaretRightIcon className="size-3 shrink-0 text-muted-foreground" />
                </RailRow>
              </section>
            )}
          </div>
        </ScrollArea>
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
