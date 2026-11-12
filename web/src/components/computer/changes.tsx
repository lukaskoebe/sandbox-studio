import { useEffect, useMemo, useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { ArrowLeftIcon, GitBranchIcon } from "@phosphor-icons/react"
import { ChangesPanel } from "@/components/chat/changes-panel"
import { requestLabel } from "@/components/chat/chat-thread"
import { FilesPanel } from "@/components/chat/files-panel"
import {
  GitChanges,
  gitChangedPaths,
  type useGitStatus,
} from "@/components/computer/git-changes"
import { GitReview, gitReviewDescription } from "@/components/git-review"
import { Button } from "@/components/ui/button"
import { ScrollArea } from "@/components/ui/scroll-area"
import { useClaimApprovals } from "@/lib/approval-claims"
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

/**
 * The screen's Changes tab, git's view of the computer (mock A): the pushes that wait for
 * review first, each opening its diff and the decision; then each repository of /workspace
 * with its branch and what is staged, not staged and untracked; then what the open thread's
 * requests changed.
 */
export function ChangesView({
  env,
  sandbox: sb,
  reviews,
  review,
  onReview,
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
  onReview: (id: string | undefined) => void
  git: ReturnType<typeof useGitStatus>
  /** Shows a file in the Files tab, relative to /workspace. */
  onOpenFile: (path: string) => void
  thread?: ScreenThread
}) {
  const changedTurns = useChangedTurns(thread)
  const [picked, setPicked] = useState<string>()
  const open = review ?? picked
  const approval = reviews.find((a) => a.id === open)
  const pick = (id: string | undefined) => {
    setPicked(id)
    onReview(id)
  }
  // The tab decides the review in view, so the floating stack leaves its card out.
  useClaimApprovals(approval ? [approval.id] : [])
  useRefreshOnTurnEnd(thread, gitPaths)
  const turn =
    changedTurns.find((t) => t.id === thread?.turnId) ?? changedTurns.at(0)

  if (approval) {
    return (
      <ScrollArea className="min-h-0 flex-1" viewportClassName="p-3 md:p-4">
        <div className="grid gap-3 text-xs">
          <Button
            variant="ghost"
            size="sm"
            className="justify-self-start text-muted-foreground"
            onClick={() => pick(undefined)}
          >
            <ArrowLeftIcon />
            Changes
          </Button>
          <p className="text-xs text-muted-foreground">
            {gitReviewDescription(approval)}
          </p>
          <GitReview key={approval.id} approval={approval} />
        </div>
      </ScrollArea>
    )
  }
  const heading = "text-caption font-medium text-muted-foreground"
  return (
    <ScrollArea className="min-h-0 flex-1" viewportClassName="p-3 md:p-4">
      <div className="grid min-w-0 gap-5 text-xs">
        {reviews.length > 0 && (
          <section aria-label="Waiting for you" className="grid min-w-0 gap-2">
            <h2 className={heading}>Waiting for you</h2>
            <ul className="grid min-w-0 gap-1">
              {reviews.map((a) => {
                const branch =
                  a.git?.review?.branch ?? a.git?.pullRequest?.head ?? "push"
                return (
                  <li
                    key={a.id}
                    className="flex min-w-0 items-center gap-2 rounded-lg border bg-card px-2.5 py-1.5"
                  >
                    <span className="size-1.5 shrink-0 rounded-full bg-(--st-waiting)" />
                    <GitBranchIcon className="size-3.5 shrink-0 text-(--acc-git)" />
                    <span className="min-w-0 flex-1 truncate">
                      Push to <span className="font-mono">{branch}</span>
                    </span>
                    <Button
                      size="xs"
                      variant="outline"
                      aria-label={`Review the push to ${branch}`}
                      onClick={() => pick(a.id)}
                    >
                      Review
                    </Button>
                  </li>
                )
              })}
            </ul>
          </section>
        )}
        <section aria-label="Git" className="grid min-w-0 gap-2">
          <GitChanges
            env={env}
            sandboxId={sb.id}
            status={git}
            onOpenFile={onOpenFile}
          />
        </section>
        {thread && (
          <section aria-label="This thread" className="grid min-w-0 gap-2">
            <h2 className={heading}>This thread</h2>
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
        )}
      </div>
    </ScrollArea>
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
