import { Badge } from "@/components/ui/badge"
import type { BuildJobStatus, BuildJobSummary } from "@/lib/api/client"

const statusLabels: Record<BuildJobStatus, string> = {
  queued: "Queued",
  preparing: "Preparing",
  setting_up: "Setting up",
  exporting: "Exporting",
  ready: "Ready",
  failed: "Failed",
  cancelled: "Cancelled",
}

const statusTones: Record<BuildJobStatus, string> = {
  queued: "bg-amber-500/10 text-amber-700 dark:text-amber-400",
  preparing: "bg-amber-500/10 text-amber-700 dark:text-amber-400",
  setting_up: "bg-amber-500/10 text-amber-700 dark:text-amber-400",
  exporting: "bg-amber-500/10 text-amber-700 dark:text-amber-400",
  ready: "bg-emerald-500/10 text-emerald-700 dark:text-emerald-400",
  failed: "bg-destructive/10 text-destructive",
  cancelled: "bg-muted text-muted-foreground",
}

export function isBuildActive(status: BuildJobStatus) {
  return (
    status === "queued" ||
    status === "preparing" ||
    status === "setting_up" ||
    status === "exporting"
  )
}

export function BuildStatusBadge({ status }: { status: BuildJobStatus }) {
  return <Badge className={statusTones[status]}>{statusLabels[status]}</Badge>
}

export function BuildCleanupStatus({
  job,
}: {
  job: Pick<BuildJobSummary, "status" | "cleanupPending" | "cleanupError">
}) {
  if (job.cleanupPending) {
    return (
      <span className="text-amber-700 dark:text-amber-400">
        {isBuildActive(job.status) ? "After build" : "In progress"}
      </span>
    )
  }
  if (job.cleanupError) {
    return <span className="text-destructive">Needs attention</span>
  }
  return <span className="text-muted-foreground">Clear</span>
}
