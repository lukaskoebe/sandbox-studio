import { Badge } from "@/components/ui/badge"
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"
import { StatusDot, type Status } from "@/components/status-dot"
import type { Approval, Connection, Rule, Sandbox } from "@/lib/api/client"
import { cn } from "@/lib/utils"

export type Phase =
  "creating" | "ready" | "booting" | "suspended" | "stopped" | "failed"

/** A sandbox's phase from its microsandbox status. A running one is ready once its agent is up. */
export function phaseOf(status: string, agent: boolean): Phase {
  switch (status) {
    case "creating":
      return "creating"
    case "running":
      return agent ? "ready" : "booting"
    case "starting":
    case "created":
      return "booting"
    case "suspended":
      return "suspended"
    // A failed create or fork stays failed until it is deleted.
    case "crashed":
    case "absent":
    case "failed":
      return "failed"
    default:
      return "stopped"
  }
}

export function phase(sb: Sandbox): Phase {
  return phaseOf(sb.status, !!sb.agent)
}

const labels: Record<Phase, string> = {
  creating: "Creating",
  ready: "Running",
  booting: "Booting",
  suspended: "Suspended",
  stopped: "Stopped",
  failed: "Failed",
}

/** The four statuses a phase shows as. Phases in progress pulse and keep their own word. */
const dots: Record<Phase, { status: Status; pulse?: boolean }> = {
  creating: { status: "running", pulse: true },
  ready: { status: "running" },
  booting: { status: "running", pulse: true },
  suspended: { status: "stopped" },
  stopped: { status: "stopped" },
  failed: { status: "failed" },
}

/** The shared StatusDot's props for a phase: its status and its own word. */
export function phaseDot(p: Phase) {
  return { ...dots[p], label: labels[p] }
}

export function StatusBadge({ sandbox }: { sandbox: Sandbox }) {
  return (
    <span title={`microsandbox status: ${sandbox.status}`}>
      <StatusDot {...phaseDot(phase(sandbox))} />
    </span>
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
