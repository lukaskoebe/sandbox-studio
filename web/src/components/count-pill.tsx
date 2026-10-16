import { cn } from "@/lib/utils"

/**
 * A small count beside a menu's name. It is quiet; `news` makes it the primary green, for
 * counts that want the user's attention, such as chats with unread replies.
 */
export function CountPill({
  count,
  news = false,
  title,
}: {
  count: number
  news?: boolean
  title?: string
}) {
  return (
    <span
      title={title}
      className={cn(
        "min-w-4 rounded-full px-1.5 text-center text-[0.625rem] tabular-nums",
        news
          ? "bg-primary text-primary-foreground"
          : "bg-muted-foreground/15 text-foreground/80"
      )}
    >
      {count}
    </span>
  )
}
