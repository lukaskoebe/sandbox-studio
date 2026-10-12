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

// Build states are not the sandbox statuses, so they get no status color: a build in progress
// is outlined, a settled one filled, and only a failed build is marked in red.
const statusTones: Record<BuildJobStatus, string> = {
  queued: "bg-background text-foreground ring-1 ring-foreground/10 ring-inset",
  preparing:
    "bg-background text-foreground ring-1 ring-foreground/10 ring-inset",
  setting_up:
    "bg-background text-foreground ring-1 ring-foreground/10 ring-inset",
  exporting:
    "bg-background text-foreground ring-1 ring-foreground/10 ring-inset",
  ready: "bg-muted text-foreground",
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
      <span>{isBuildActive(job.status) ? "After build" : "In progress"}</span>
    )
  }
  if (job.cleanupError) {
    return <span className="text-destructive">Needs attention</span>
  }
  return <span className="text-muted-foreground">Clear</span>
}
