import { useEffect, useMemo, useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { GitBranchIcon } from "@phosphor-icons/react"
import { ChangesPanel } from "@/components/chat/changes-panel"
import { requestLabel } from "@/components/chat/chat-thread"
import { FilesPanel } from "@/components/chat/files-panel"
import { GitReview, gitReviewDescription } from "@/components/git-review"
import { ScrollArea } from "@/components/ui/scroll-area"
import { useClaimApprovals } from "@/lib/approval-claims"
import type { Approval, Sandbox } from "@/lib/api/client"
import {
  changeCount,
  type Chat,
  type ChatItem,
  type ChatTurn,
} from "@/lib/chats"
import { cn } from "@/lib/utils"

type View = { kind: "review"; id: string } | { kind: "changes" | "files" }

/**
 * The screen's Changes tab: a push that waits for review comes first, with its diff and the
 * decision; then what the open thread changed, request by request; then the computer's files.
 */
export function ChangesView({
  env,
  sandbox: sb,
  reviews,
  review,
  onReview,
  thread,
}: {
  env: string
  sandbox: Sandbox
  /** The pushes from this computer that wait for review. */
  reviews: Approval[]
  /** The review a link names. */
  review?: string
  onReview: (id: string | undefined) => void
  thread?: {
    chat: Chat
    turns: ChatTurn[]
    items: ChatItem[]
    turnId?: string
    onTurn: (id: string) => void
  }
}) {
  const queryClient = useQueryClient()
  const changedTurns = useMemo(
    () =>
      (thread?.turns ?? [])
        .filter((t) => t.endedAt && changeCount(t) > 0)
        .reverse(),
    [thread?.turns]
  )
  const [picked, setPicked] = useState<View>()
  const view: View =
    review && reviews.some((a) => a.id === review)
      ? { kind: "review", id: review }
      : ((picked?.kind === "review" && !reviews.some((a) => a.id === picked.id)
          ? undefined
          : picked) ??
        (reviews[0]
          ? { kind: "review", id: reviews[0].id }
          : changedTurns.length > 0
            ? { kind: "changes" }
            : { kind: "files" }))
  const pick = (v: View) => {
    setPicked(v)
    onReview(v.kind === "review" ? v.id : undefined)
  }
  // The tab decides the review in view, so the floating stack leaves its card out.
  useClaimApprovals(view.kind === "review" ? [view.id] : [])

  const [where, setWhere] = useState<{ dir: string; file?: string }>({
    dir: "",
  })
  const turn =
    changedTurns.find((t) => t.id === thread?.turnId) ?? changedTurns.at(0)
  const changed = useMemo(
    () => new Set((turn?.changes?.files ?? []).map((f) => f.path)),
    [turn]
  )
  // Files may change whenever a request ends.
  const ended = (thread?.turns ?? []).filter((t) => t.endedAt).length
  useEffect(() => {
    for (const path of [
      "/api/environments/{env}/sandboxes/{id}/files",
      "/api/environments/{env}/sandboxes/{id}/files/content",
    ])
      void queryClient.invalidateQueries({ queryKey: ["get", path] })
  }, [ended, queryClient])

  const approval =
    view.kind === "review" ? reviews.find((a) => a.id === view.id) : undefined
  const chip = (on: boolean) =>
    cn(
      "inline-flex h-6 shrink-0 items-center gap-1.5 rounded-full border px-2.5 text-xs",
      on
        ? "border-foreground/20 bg-muted font-medium text-foreground"
        : "text-muted-foreground hover:text-foreground"
    )

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex shrink-0 items-center gap-1.5 overflow-x-auto border-b px-3 py-2">
        {reviews.map((a) => {
          const branch =
            a.git?.review?.branch ?? a.git?.pullRequest?.head ?? "push"
          return (
            <button
              key={a.id}
              type="button"
              aria-pressed={view.kind === "review" && view.id === a.id}
              className={chip(view.kind === "review" && view.id === a.id)}
              onClick={() => pick({ kind: "review", id: a.id })}
            >
              <GitBranchIcon className="size-3.5 text-(--acc-git)" />
              Push to <span className="font-mono">{branch}</span>
              <span
                aria-label="waiting for you"
                className="size-1.5 rounded-full bg-(--st-waiting)"
              />
            </button>
          )
        })}
        {thread && (
          <button
            type="button"
            aria-pressed={view.kind === "changes"}
            className={chip(view.kind === "changes")}
            onClick={() => pick({ kind: "changes" })}
          >
            In this thread
            {changedTurns.length > 0 && (
              <span className="font-mono text-meta text-muted-foreground">
                {changedTurns.length}
              </span>
            )}
          </button>
        )}
        <button
          type="button"
          aria-pressed={view.kind === "files"}
          className={chip(view.kind === "files")}
          onClick={() => pick({ kind: "files" })}
        >
          Files
        </button>
      </div>
      <ScrollArea className="min-h-0 flex-1" viewportClassName="p-3 md:p-4">
        {approval ? (
          <div className="grid gap-3 text-xs">
            <p className="text-xs text-muted-foreground">
              {gitReviewDescription(approval)}
            </p>
            <GitReview key={approval.id} approval={approval} />
          </div>
        ) : view.kind === "changes" && thread ? (
          <ChangesPanel
            env={env}
            chatId={thread.chat.id}
            turns={changedTurns}
            turnId={turn?.id}
            onTurnChange={thread.onTurn}
            label={(t) => requestLabel(thread.items, t)}
          />
        ) : view.kind === "changes" ? (
          <p className="py-6 text-center text-xs text-muted-foreground">
            Open a thread to see what it changed.
          </p>
        ) : (
          <FilesPanel
            env={env}
            sandboxId={sb.id}
            dir={where.dir}
            file={where.file}
            onOpen={(dir, file) => setWhere({ dir, file })}
            changed={changed}
          />
        )}
      </ScrollArea>
    </div>
  )
}
