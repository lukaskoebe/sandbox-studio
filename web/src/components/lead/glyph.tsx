import { CompassIcon } from "@phosphor-icons/react"
import { cn } from "@/lib/utils"

/**
 * Lead's avatar: a compass in a round tile where a teammate has its face, since Lead has no
 * computer and no face of its own (docs/lead.md).
 */
export function LeadGlyph({ className }: { className?: string }) {
  return (
    <span
      aria-hidden
      data-slot="avatar"
      className={cn(
        "inline-flex size-6 shrink-0 items-center justify-center rounded-full bg-foreground text-background",
        className
      )}
    >
      <CompassIcon weight="bold" className="size-3/5" />
    </span>
  )
}
