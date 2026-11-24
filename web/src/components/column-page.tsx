import type { ReactNode } from "react"
import { ScrollArea } from "@/components/ui/scroll-area"
import { cn } from "@/lib/utils"

/**
 * A page that reads top to bottom: its title, actions and explanation over one centered column,
 * with no rule under the header, as the teammate page's.
 */
export function ColumnPage({
  title,
  actions,
  intro,
  narrow,
  titleHidden,
  back,
  children,
}: {
  title: string
  /**
   * The crumb already names the page: the title is for screen readers, and the intro takes its
   * place at the top.
   */
  titleHidden?: boolean
  actions?: ReactNode
  /** What the page is for, or where things stand, in a sentence or two. */
  intro?: ReactNode
  /** A column for reading rather than for tables. */
  narrow?: boolean
  /** A way back above the title, such as "← Lead". */
  back?: ReactNode
  children: ReactNode
}) {
  const column = cn("mx-auto w-full", narrow ? "max-w-3xl" : "max-w-4xl")
  return (
    <>
      <header data-slot="page-header" className="shrink-0 px-4 md:px-6">
        {back && <div className={cn(column, "pt-4")}>{back}</div>}
        <div className={cn(column, "flex h-14 items-center gap-2")}>
          <h1 className={titleHidden ? "sr-only" : "text-sm"}>{title}</h1>
          {titleHidden && intro && (
            <p className="min-w-0 truncate text-sm text-muted-foreground">
              {intro}
            </p>
          )}
          {actions && (
            <div className="ml-auto flex items-center gap-1">{actions}</div>
          )}
        </div>
      </header>
      <ScrollArea
        className="min-h-0 flex-1"
        viewportClassName="px-4 pb-12 md:px-6"
      >
        <div className={cn(column, "grid grid-cols-1 content-start gap-6")}>
          {intro && !titleHidden && (
            <p className="max-w-2xl text-xs/relaxed text-muted-foreground">
              {intro}
            </p>
          )}
          {children}
        </div>
      </ScrollArea>
    </>
  )
}
