import { useEffect, useRef, type ReactNode } from "react"
import { CaretRightIcon, type Icon } from "@phosphor-icons/react"
import { InDisclosure } from "@/components/section-header"
import { cn } from "@/lib/utils"

/**
 * A row in the look of the profile card's settings: an icon, the label, the current value and
 * a caret. It opens to its fields underneath, indented on a hairline. The fields stay mounted
 * while closed, so what was typed in them survives closing the row. A row that is open when it
 * first shows, as one picked on the profile card is, scrolls into view.
 */
export function DisclosureRow({
  id,
  icon: I,
  title,
  summary,
  open,
  onOpenChange,
  destructive,
  children,
}: {
  id: string
  icon: Icon
  title: string
  summary?: ReactNode
  open: boolean
  onOpenChange: (open: boolean) => void
  destructive?: boolean
  children: ReactNode
}) {
  const row = useRef<HTMLButtonElement>(null)
  const openedFirst = useRef(open)
  useEffect(() => {
    if (openedFirst.current) row.current?.scrollIntoView({ block: "nearest" })
  }, [])
  return (
    <div className="min-w-0">
      <button
        ref={row}
        type="button"
        aria-expanded={open}
        aria-controls={`${id}-panel`}
        onClick={() => onOpenChange(!open)}
        className={cn(
          "flex min-h-8 w-full min-w-0 items-center gap-2.5 rounded-md px-2.5 py-1 text-left text-title outline-none hover:bg-muted focus-visible:bg-muted focus-visible:ring-2 focus-visible:ring-ring/40",
          open && "bg-muted",
          destructive && "text-destructive"
        )}
      >
        <I
          aria-hidden
          className={cn(
            "size-3.5 shrink-0",
            destructive ? "text-destructive" : "text-muted-foreground"
          )}
        />
        <span className="shrink-0">{title}</span>
        <span
          title={typeof summary === "string" ? summary : undefined}
          className="ml-auto min-w-0 truncate text-xs text-muted-foreground"
        >
          {summary}
        </span>
        <CaretRightIcon
          aria-hidden
          className={cn(
            "size-3 shrink-0 text-muted-foreground transition-transform motion-reduce:transition-none",
            open && "rotate-90"
          )}
        />
      </button>
      <div
        id={`${id}-panel`}
        hidden={!open}
        className="mt-1 mb-3 ml-[1.1rem] grid gap-4 border-l py-2 pr-1 pl-5"
      >
        <InDisclosure.Provider value={true}>{children}</InDisclosure.Provider>
      </div>
    </div>
  )
}

/** A labelled group of disclosure rows, as the profile card groups its rows. */
export function DisclosureGroup({
  title,
  children,
}: {
  title: string
  children: ReactNode
}) {
  return (
    <section
      aria-label={title}
      className="grid gap-0.5 border-b pb-3 not-first:pt-3 last:border-b-0"
    >
      <h3 className="flex h-6 items-center px-2.5 section-label text-muted-foreground">
        {title}
      </h3>
      {children}
    </section>
  )
}
