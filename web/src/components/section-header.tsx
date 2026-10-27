import { createContext, useContext, type ReactNode } from "react"
import type { Icon } from "@phosphor-icons/react"
import { tileClass, type Area } from "@/lib/accents"
import { cn } from "@/lib/utils"

/**
 * Whether the section sits in a disclosure card, which shows the title itself: the header then
 * keeps the title for screen readers only, and its actions.
 */
export const InDisclosure = createContext(false)

/**
 * A section's title row: a small icon tile, a semibold title, a count in mono and actions on
 * the right. The tile takes the area's hue when the section belongs to one. The title is an
 * h2, so the page's own title stays the h1.
 */
export function SectionHeader({
  title,
  icon: IconComp,
  area,
  count,
  actions,
  className,
}: {
  title: ReactNode
  icon?: Icon
  area?: Area
  count?: ReactNode
  actions?: ReactNode
  className?: string
}) {
  if (useContext(InDisclosure))
    return (
      <>
        <h2 className="sr-only">{title}</h2>
        {actions && (
          <div className="flex items-center justify-end gap-1">{actions}</div>
        )}
      </>
    )
  return (
    <div className={cn("flex h-6 items-center gap-2", className)}>
      {IconComp && (
        <span
          className={cn(
            "inline-flex size-5 shrink-0 items-center justify-center rounded-sm",
            tileClass(area)
          )}
        >
          <IconComp weight="bold" className="size-3" aria-hidden />
        </span>
      )}
      <h2 className="font-heading text-title font-semibold">{title}</h2>
      {count !== undefined && (
        <span className="font-mono text-caption text-muted-foreground">
          {count}
        </span>
      )}
      {actions && (
        <div className="ml-auto flex items-center gap-1">{actions}</div>
      )}
    </div>
  )
}
