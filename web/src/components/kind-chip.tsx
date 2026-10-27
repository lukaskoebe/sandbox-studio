import type { ReactNode } from "react"
import type { Icon } from "@phosphor-icons/react"
import { tileClass, type Area } from "@/lib/accents"
import { cn } from "@/lib/utils"

/**
 * An approval kind's name with its icon. The icon sits on a tile in the area's hue; the name
 * stays in body text, so the chip stays legible in both themes.
 */
export function KindChip({
  area,
  icon: IconComp,
  children,
  className,
}: {
  area?: Area
  icon?: Icon
  children: ReactNode
  className?: string
}) {
  return (
    <span
      className={cn(
        "inline-flex h-5 max-w-full shrink-0 items-center gap-1.5 rounded-sm bg-muted py-0.5 pr-2 pl-0.5 text-caption font-medium",
        className
      )}
    >
      {IconComp && (
        <span
          className={cn(
            "inline-flex size-4 shrink-0 items-center justify-center rounded-sm",
            tileClass(area)
          )}
        >
          <IconComp weight="bold" className="size-2.5" aria-hidden />
        </span>
      )}
      <span className="truncate">{children}</span>
    </span>
  )
}
