import { cn } from "@/lib/utils"

/**
 * How many approvals wait for you, as a count on a chip in the waiting color. It shows nothing
 * at zero, so a row carries the count only while there is one to answer.
 */
export function WaitingCount({
  count,
  className,
}: {
  count: number
  className?: string
}) {
  if (count === 0) return null
  return (
    <span
      data-status="waiting"
      title={`${count} waiting for you`}
      className={cn(
        "inline-flex h-4 min-w-4 shrink-0 items-center justify-center rounded-sm bg-(--st-waiting)/25 px-1 font-mono text-meta font-semibold text-foreground",
        className
      )}
    >
      {count}
    </span>
  )
}
