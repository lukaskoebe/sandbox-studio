import { Badge } from "@/components/ui/badge"
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"
import type { Approval, Connection, Rule, Sandbox } from "@/lib/api/client"
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

const tones = {
  blue: "bg-sky-500/10 text-sky-700 dark:text-sky-400",
  green: "bg-emerald-500/10 text-emerald-700 dark:text-emerald-400",
  amber: "bg-amber-500/10 text-amber-700 dark:text-amber-400",
  red: "bg-destructive/10 text-destructive",
  muted: "bg-muted text-muted-foreground",
}

const approvalTones: Record<Approval["status"], string> = {
  pending: tones.amber,
  approved: tones.green,
  denied: tones.red,
  dismissed: tones.muted,
}

export function ApprovalStatusBadge({
  status,
}: {
  status: Approval["status"]
}) {
  return (
    <Badge className={cn("capitalize", approvalTones[status])}>{status}</Badge>
  )
}

const verdictTones: Record<Connection["verdict"], string> = {
  open: tones.blue,
  allowed: tones.green,
  denied: tones.red,
  undecided: tones.amber,
  pending: tones.amber,
  failed: tones.muted,
}

/** A failed connection explains itself in a tooltip. */
export function VerdictBadge({ conn }: { conn: Connection }) {
  const badge = (
    <Badge className={cn("capitalize", verdictTones[conn.verdict])}>
      {conn.verdict}
    </Badge>
  )
  if (conn.verdict !== "failed" || !conn.error) return badge
  return (
    <Tooltip>
      <TooltipTrigger render={<span className="inline-flex" />}>
        {badge}
      </TooltipTrigger>
      <TooltipContent>{conn.error}</TooltipContent>
    </Tooltip>
  )
}

const actionTones: Record<Rule["action"], string> = {
  allow: tones.green,
  deny: tones.red,
  proxy: tones.blue,
  caddy: tones.muted,
}

export function RuleActionBadge({ action }: { action: Rule["action"] }) {
  return <Badge className={actionTones[action]}>{action}</Badge>
}
