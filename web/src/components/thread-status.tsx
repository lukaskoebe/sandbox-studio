import { usePendingApprovals } from "@/hooks/use-pending-approvals"
import type { Chat } from "@/lib/chats"
import {
  chatApprovals,
  chatStatus,
  statusLabels,
  type ThreadStatus,
} from "@/lib/threads"
import { cn } from "@/lib/utils"

/** A thread's state as a dot, the way the sidebar and the desk list threads. */
export function ThreadDot({
  status,
  className,
}: {
  status: ThreadStatus
  className?: string
}) {
  if (status === "terminal")
    return (
      <span
        aria-hidden
        className={cn(
          "shrink-0 font-mono text-meta leading-none font-semibold text-muted-foreground",
          className
        )}
      >
        &gt;_
      </span>
    )
  return (
    <span
      aria-hidden
      className={cn(
        "size-2 shrink-0 rounded-full",
        status === "working" && "animate-pulse bg-(--st-running)",
        status === "waiting" && "bg-(--st-waiting)",
        (status === "done" || status === "ended") &&
          "ring-1 ring-muted-foreground/60 ring-inset",
        className
      )}
    />
  )
}

/**
 * A thread's state as a chip, in the words the sidebar uses: in the waiting colour when it
 * needs you. The top bar shows it after an open thread's title, the Chats table in its rows.
 * With compact, a narrow window shows the dot alone.
 */
export function ThreadStatusChip({
  status,
  compact = false,
}: {
  status: ThreadStatus
  compact?: boolean
}) {
  return (
    <span
      data-testid="thread-status"
      className={cn(
        "inline-flex h-5 shrink-0 items-center gap-1.5 rounded-full border px-2 text-caption font-normal whitespace-nowrap text-muted-foreground",
        compact && "max-md:px-1.5",
        status === "waiting" &&
          "border-(--st-waiting)/45 bg-(--st-waiting)/12 text-foreground"
      )}
    >
      <ThreadDot
        status={status}
        className={cn(compact && status === "waiting" && "md:hidden")}
      />
      <span className={cn(compact && "max-md:sr-only")}>
        {statusLabels[status]}
      </span>
    </span>
  )
}

/** A chat's state as a chip, counting the permission asks that wait in it. */
export function ChatStatusChip({ chat }: { chat: Chat }) {
  const pending = usePendingApprovals()
  const asks = chatApprovals(chat, pending.data ?? []).length
  return <ThreadStatusChip status={chatStatus(chat, asks)} />
}
