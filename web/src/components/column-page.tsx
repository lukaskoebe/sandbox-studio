import type { ReactNode } from "react"
import { ScrollArea } from "@/components/ui/scroll-area"
import { SidebarTrigger } from "@/components/ui/sidebar"
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
  toggle = true,
  children,
}: {
  title: string
  actions?: ReactNode
  /** What the page is for, or where things stand, in a sentence or two. */
  intro?: ReactNode
  /** A column for reading rather than for tables. */
  narrow?: boolean
  /** Whether the header has the sidebar's toggle on phones. */
  toggle?: boolean
  children: ReactNode
}) {
  const column = cn("mx-auto w-full", narrow ? "max-w-3xl" : "max-w-4xl")
  return (
    <>
      <header data-slot="page-header" className="shrink-0 px-4 md:px-6">
        <div className={cn(column, "flex h-14 items-center gap-2")}>
          {toggle && <SidebarTrigger className="md:hidden" />}
          <h1 className="text-sm">{title}</h1>
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
          {intro && (
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
