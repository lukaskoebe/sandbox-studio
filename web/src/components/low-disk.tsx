import { useState } from "react"
import { HardDriveIcon } from "@phosphor-icons/react"
import { Button } from "@/components/ui/button"
import { ResizeDisksDialog } from "@/components/resize-disks-dialog"
import type { Sandbox } from "@/lib/api/client"
import type { components } from "@/lib/api/schema.gen"
import { formatBytes } from "@/lib/utils"

type Disk = components["schemas"]["Disk"]

const diskNames: Record<Disk["name"], string> = {
  home: "home disk",
  workspace: "workspace disk",
  docker: "Docker disk",
}

/** "The home disk has 180 MB of 4.0 GB free", for each nearly full disk. */
export function lowDiskText(disks: Disk[]) {
  return disks
    .map(
      (d) =>
        `The ${diskNames[d.name]} has ${formatBytes(d.free)} of ${formatBytes(d.total)} free`
    )
    .join(". ")
}

/**
 * What to do about each nearly full disk: agents stop when the home disk fills, and Docker's
 * builds and pulls fail when its disk does.
 */
function advice(disks: Disk[]) {
  const has = (name: Disk["name"]) => disks.some((d) => d.name === name)
  return (
    <>
      {has("home") && (
        <>
          Agents stop when the home disk fills: free some space in the terminal,
          such as with <code className="font-mono">rm -rf ~/.cache ~/.npm</code>
          .{" "}
        </>
      )}
      {has("workspace") && "Free some space in /workspace from the terminal. "}
      {has("docker") && (
        <>
          Free Docker&apos;s space with{" "}
          <code className="font-mono">docker builder prune</code> and{" "}
          <code className="font-mono">docker image prune -a</code>.
        </>
      )}
    </>
  )
}

/**
 * A strip above a sandbox's screen or chat while one of its disks is nearly full, as Studio
 * last heard from the sandbox, with a way to give the disks more room.
 */
export function LowDiskStatus({
  env,
  sandbox,
}: {
  env: string
  sandbox?: Sandbox
}) {
  const [resizing, setResizing] = useState(false)
  const disks = sandbox?.lowDisks
  if (!sandbox || !disks?.length) return null
  const available = sandbox.status === "running" || sandbox.status === "stopped"
  return (
    <div
      role="status"
      aria-label="Disk space"
      className="flex shrink-0 items-center gap-2 border-b bg-(--st-waiting)/10 px-3 py-1 text-xs"
    >
      <HardDriveIcon className="size-3 shrink-0 text-(--st-waiting-fg) dark:text-(--st-waiting)" />
      <span>
        {lowDiskText(disks)}. {advice(disks)}
      </span>
      {available && (
        <Button
          variant="outline"
          size="xs"
          className="ml-auto shrink-0"
          onClick={() => setResizing(true)}
        >
          Resize disks…
        </Button>
      )}
      <ResizeDisksDialog
        env={env}
        sandbox={sandbox}
        open={resizing}
        onOpenChange={setResizing}
      />
    </div>
  )
}
