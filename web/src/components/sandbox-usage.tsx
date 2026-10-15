import { $api, type Sandbox, type SandboxUsage } from "@/lib/api/client"
import { cn } from "@/lib/utils"

/** msb samples a VM every few seconds; polling faster would show nothing new. */
const pollMs = 5000

const MiB = 1 << 20
const GiB = 1 << 30

/** A memory size in GiB with at most one decimal, or in MiB below 1 GiB: 1.2, 4, 512. */
function amount(bytes: number, unit: number) {
  const v = bytes / unit
  return unit === GiB ? v.toFixed(1).replace(/\.0$/, "") : String(Math.round(v))
}

/** "cpu 12% · mem 1.2/4 GiB": a running sandbox's use, compact for the status bar. */
export function usageLabel(u: SandboxUsage) {
  const unit = u.memoryTotalBytes >= GiB ? GiB : MiB
  const mem = `${amount(u.memoryBytes, unit)}/${amount(u.memoryTotalBytes, unit)} ${unit === GiB ? "GiB" : "MiB"}`
  return `cpu ${Math.round(u.cpuPercent)}% · mem ${mem}`
}

function usageTitle(u: SandboxUsage) {
  const cpus = u.cpus === 1 ? "1 CPU" : `${u.cpus} CPUs`
  return `CPU ${Math.round(u.cpuPercent)}% of ${cpus} over the last few seconds. Memory ${amount(u.memoryBytes, MiB)} MiB used of ${amount(u.memoryTotalBytes, MiB)} MiB.`
}

/**
 * A sandbox's CPU and memory use, polled while it runs. It shows nothing for a sandbox that
 * doesn't run, or before the first answer.
 */
export function SandboxUsageText({
  env,
  sandbox: sb,
  className,
}: {
  env: string
  sandbox: Sandbox
  className?: string
}) {
  const running = sb.status === "running"
  const usage = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes/{id}/usage",
    { params: { path: { env, id: sb.id } } },
    {
      enabled: running,
      refetchInterval: running ? pollMs : false,
      retry: false,
    }
  )
  if (!running || !usage.data) return null
  return (
    <span
      className={cn("tabular-nums", className)}
      title={usageTitle(usage.data)}
    >
      {usageLabel(usage.data)}
    </span>
  )
}
