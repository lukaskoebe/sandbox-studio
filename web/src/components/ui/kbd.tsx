import type { ComponentProps } from "react"
import { cn } from "@/lib/utils"

/** A key or shortcut as the keyboard shows it, such as ⌘K. */
export function Kbd({ className, ...props }: ComponentProps<"kbd">) {
  return (
    <kbd
      className={cn(
        "pointer-events-none inline-flex h-4 shrink-0 items-center rounded-sm border border-border bg-muted px-1 font-mono text-meta text-muted-foreground",
        className
      )}
      {...props}
    />
  )
}
