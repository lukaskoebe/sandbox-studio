import { ListChecksIcon } from "@phosphor-icons/react"
import { Checkbox } from "@/components/ui/checkbox"
import type { components } from "@/lib/api/schema.gen"
import { cn } from "@/lib/utils"

type PlanEntry = components["schemas"]["PlanEntry"]

/** The agent's plan as a read-only checklist; the step in progress shimmers while live. */
export function PlanCard({
  entries,
  live = false,
  className,
}: {
  entries: PlanEntry[]
  live?: boolean
  className?: string
}) {
  const done = entries.filter((e) => e.status === "completed").length
  return (
    <div
      className={cn(
        "grid min-w-0 gap-2 rounded-lg border bg-card p-2.5 text-xs",
        className
      )}
    >
      <p className="flex items-center gap-1.5 font-medium">
        <ListChecksIcon className="size-3.5 text-muted-foreground" />
        Plan
        <span className="ml-auto font-normal text-muted-foreground tabular-nums">
          {done} of {entries.length} done
        </span>
      </p>
      <ul className="grid gap-1.5">
        {entries.map((e, i) => (
          <li key={i} className="flex items-start gap-2">
            <Checkbox
              checked={e.status === "completed"}
              readOnly
              tabIndex={-1}
              aria-label={e.status === "completed" ? "Done" : "Not done yet"}
              className="mt-0.5"
            />
            <span
              className={cn(
                "min-w-0 wrap-break-word",
                e.status === "completed" && "text-muted-foreground",
                e.status === "in_progress" && live && "shimmer"
              )}
            >
              {e.content}
            </span>
          </li>
        ))}
      </ul>
    </div>
  )
}
