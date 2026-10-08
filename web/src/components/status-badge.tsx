import { Badge } from "@/components/ui/badge"
import type { Sandbox } from "@/lib/api/client"
import { cn } from "@/lib/utils"

type Phase = "ready" | "booting" | "stopped" | "failed"

export function phase(sb: Sandbox): Phase {
  switch (sb.status) {
    case "running":
      return sb.agent ? "ready" : "booting"
    case "starting":
    case "created":
      return "booting"
    case "crashed":
    case "absent":
      return "failed"
    default:
      return "stopped"
  }
}

const labels: Record<Phase, string> = {
  ready: "Running",
  booting: "Booting",
  stopped: "Stopped",
  failed: "Failed",
}

const dots: Record<Phase, string> = {
  ready: "bg-emerald-500",
  booting: "bg-amber-500 animate-pulse",
  stopped: "bg-muted-foreground/40",
  failed: "bg-destructive",
}

export function StatusDot({
  sandbox,
  className,
}: {
  sandbox: Sandbox
  className?: string
}) {
  return (
    <span
      className={cn(
        "size-2 shrink-0 rounded-full",
        dots[phase(sandbox)],
        className
      )}
    />
  )
}

export function StatusBadge({ sandbox }: { sandbox: Sandbox }) {
  const p = phase(sandbox)
  return (
    <Badge variant="outline" title={`microsandbox status: ${sandbox.status}`}>
      <StatusDot sandbox={sandbox} />
      {labels[p]}
    </Badge>
  )
}
