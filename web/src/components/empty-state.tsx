import type { ReactNode } from "react"
import type { Icon } from "@phosphor-icons/react"
import {
  Empty,
  EmptyContent,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty"
import { cn } from "@/lib/utils"

/**
 * A page's list while it is empty, the same everywhere: an icon, a title, one sentence and
 * one button. While it shows, the page's header leaves its own button out, so there is one
 * way to add the first item; `more` is a second, quieter way, such as "or import one".
 * Small lists inside a page keep a line of muted text instead.
 */
export function EmptyState({
  icon: I,
  title,
  children,
  action,
  more,
  className,
}: {
  icon: Icon
  title: string
  /** One sentence: what goes here, not how the page works. */
  children: ReactNode
  action?: ReactNode
  more?: ReactNode
  className?: string
}) {
  return (
    <Empty
      role="region"
      aria-label={title}
      className={cn("flex-none gap-3 border px-4 py-8", className)}
    >
      <EmptyHeader className="gap-1.5">
        <EmptyMedia
          variant="icon"
          className="mb-1 rounded-(--radius) border bg-card"
        >
          <I />
        </EmptyMedia>
        <EmptyTitle>{title}</EmptyTitle>
        <EmptyDescription>{children}</EmptyDescription>
      </EmptyHeader>
      {(action || more) && (
        <EmptyContent className="gap-1.5">
          {action}
          {more && (
            <span className="text-caption text-muted-foreground">{more}</span>
          )}
        </EmptyContent>
      )}
    </Empty>
  )
}
