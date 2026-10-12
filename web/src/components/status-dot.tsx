import { cn } from "@/lib/utils"

/** The four statuses. Their colors are fixed and never used for an area. */
export type Status = "running" | "waiting" | "failed" | "stopped"

const words: Record<Status, string> = {
  running: "Running",
  waiting: "Waiting for you",
  failed: "Failed",
  stopped: "Stopped",
}

const dots: Record<Status, string> = {
  running: "bg-(--st-running)",
  waiting: "bg-(--st-waiting)",
  failed: "bg-(--st-failed)",
  stopped: "ring-1 ring-(--st-stopped) ring-inset",
}

/**
 * A status as a dot and its word. The word is always shown, in body text: the color adds to
 * it and never carries the state alone, and text is never set in a status color. `label`
 * replaces the word for a state the four do not name, such as "Booting". Dense lists show the
 * dot alone (`compact`): the word becomes its title and the text screen readers read.
 */
export function StatusDot({
  status,
  label = words[status],
  pulse = false,
  compact = false,
  className,
}: {
  status: Status
  label?: string
  /** For a state in progress, such as a sandbox that is booting. */
  pulse?: boolean
  compact?: boolean
  className?: string
}) {
  return (
    <span
      data-status={status}
      title={compact ? label : undefined}
      className={cn("inline-flex items-center gap-1.5", className)}
    >
      <span
        aria-hidden="true"
        className={cn(
          "inline-block size-2 shrink-0 rounded-full",
          dots[status],
          pulse && "animate-pulse"
        )}
      />
      {compact ? <span className="sr-only">{label}</span> : label}
    </span>
  )
}
