import { StatusDot, type Status } from "@/components/status-dot"
import type { Sandbox } from "@/lib/api/client"

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
