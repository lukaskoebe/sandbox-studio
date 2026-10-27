import type { ReactNode } from "react"
import { CaretRightIcon } from "@phosphor-icons/react"
import { InDisclosure } from "@/components/section-header"
import { cn } from "@/lib/utils"

/**
 * A card that shows its title and a one-line summary, and opens to its fields. The fields stay
 * mounted while closed, so what was typed in them survives closing the card.
 */
export function DisclosureCard({
  id,
  title,
  summary,
  open,
  onOpenChange,
  children,
}: {
  id: string
  title: string
  summary?: ReactNode
  open: boolean
  onOpenChange: (open: boolean) => void
  children: ReactNode
}) {
  return (
    <div className="min-w-0 rounded-lg bg-card ring-1 ring-foreground/10">
      <button
        type="button"
        aria-expanded={open}
        aria-controls={`${id}-panel`}
        onClick={() => onOpenChange(!open)}
        className="flex w-full items-center gap-3 rounded-lg px-4 py-3 text-left outline-none focus-visible:ring-2 focus-visible:ring-ring"
      >
        <span className="w-32 shrink-0 text-title font-semibold sm:w-40">
          {title}
        </span>
        <span
          title={typeof summary === "string" ? summary : undefined}
          className="min-w-0 flex-1 truncate text-xs text-muted-foreground"
        >
          {summary}
        </span>
        <CaretRightIcon
          aria-hidden
          className={cn(
            "size-3 shrink-0 text-muted-foreground transition-transform",
            open && "rotate-90"
          )}
        />
      </button>
      <div id={`${id}-panel`} hidden={!open} className="grid gap-4 px-4 pb-4">
        <InDisclosure.Provider value={true}>{children}</InDisclosure.Provider>
      </div>
    </div>
  )
}
