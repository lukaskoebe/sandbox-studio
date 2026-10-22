import { useEffect, useState, type ReactNode } from "react"
import {
  ArrowSquareOutIcon,
  BroomIcon,
  CaretDownIcon,
  CpuIcon,
  FloppyDiskIcon,
} from "@phosphor-icons/react"
import {
  CheckpointsSection,
  SaveCheckpointDialog,
} from "@/components/checkpoints-section"
import { Ident } from "@/components/ident"
import { LineageTimeline } from "@/components/lineage-timeline"
import { ResizeSandboxDialog } from "@/components/resize-sandbox-dialog"
import {
  lostVM,
  StartButton,
  StopButton,
  wakeAction,
} from "@/components/sandbox-lifecycle"
import { phase, phaseDot } from "@/components/status-badge"
import { StatusDot } from "@/components/status-dot"
import { SandboxOwner } from "@/components/teammate-avatar"
import { Button } from "@/components/ui/button"
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"
import { ScrollArea } from "@/components/ui/scroll-area"
import { $api, isReady, type Sandbox } from "@/lib/api/client"
import {
  freshPreviewHint,
  onStudioMachine,
  openPreviewTab,
} from "@/lib/previews"
import { formatAge, formatUptime } from "@/lib/utils"

export function formatMiB(mib: number) {
  return mib >= 1024 ? `${+(mib / 1024).toFixed(1)} GiB` : `${mib} MiB`
}

/**
 * The sandbox's facts behind one pill in its header: its state and size on the pill, and on
 * click the owner, template, resources, age and ports, with start, stop and resize.
 */
export function SandboxDetails({
  env,
  sandbox: sb,
}: {
  env: string
  sandbox: Sandbox
}) {
  const p = phase(sb)
  const dot = phaseDot(p)
  const [resizing, setResizing] = useState(false)
  // Uptime is relative to now, so the clock is read again every minute.
  const [now, setNow] = useState(Date.now)
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 60_000)
    return () => clearInterval(timer)
  }, [])
  const note =
    p === "stopped"
      ? "Your workspace, home directory and Docker images are kept."
      : p === "suspended"
        ? "Suspended: resume it to continue where it left off."
        : sb.status === "absent"
          ? lostVM
          : p === "failed"
            ? "The sandbox stopped unexpectedly. Your workspace is kept. Start it again to continue."
            : undefined
  return (
    <>
      <Popover>
        <PopoverTrigger
          render={
            <button
              type="button"
              aria-label="Sandbox details"
              className="inline-flex h-6 shrink-0 items-center gap-1.5 rounded-full border bg-muted/40 px-2 text-xs hover:bg-muted"
            />
          }
        >
          <span title={`microsandbox status: ${sb.status}`}>
            <StatusDot {...dot} className="shrink-0" />
          </span>
          <span className="text-muted-foreground max-md:hidden">
            · {sb.cpus} CPU · {formatMiB(sb.memoryMiB)}
          </span>
          <CaretDownIcon className="size-3 text-muted-foreground" />
        </PopoverTrigger>
        <PopoverContent className="grid w-80 gap-3">
          {note && <p className="text-muted-foreground">{note}</p>}
          <dl className="grid grid-cols-[6rem_minmax(0,1fr)] gap-x-3 gap-y-1.5">
            <Fact label="Teammate">
              {sb.teammateId ? (
                <SandboxOwner env={env} teammateId={sb.teammateId} />
              ) : (
                <span className="text-muted-foreground">None</span>
              )}
            </Fact>
            <Fact label="Template">
              <span className="font-mono" title={sb.templateId || undefined}>
                {sb.templateName ?? sb.templateId ?? "Base image"}
              </span>
            </Fact>
            <Fact label="Resources">
              {sb.cpus} CPU · {formatMiB(sb.memoryMiB)} ·{" "}
              {formatMiB(sb.workspaceMiB)} disk
            </Fact>
            {p === "ready" && sb.startedAt && (
              <Fact label="Up">
                {formatUptime(sb.startedAt, now).replace(/^Up /, "")}
              </Fact>
            )}
            <Fact label="Created">
              <time
                dateTime={sb.createdAt}
                title={new Date(sb.createdAt).toLocaleString()}
              >
                {formatAge(sb.createdAt)}
              </time>
            </Fact>
            <Fact label="ID">
              <Ident copy>{sb.id}</Ident>
            </Fact>
            {isReady(sb) && (
              <Fact label="Ports">
                <Ports env={env} sandbox={sb} />
              </Fact>
            )}
          </dl>
          <div className="flex flex-wrap gap-2">
            {wakeAction(sb) === "start" && (
              <StartButton env={env} sandbox={sb} />
            )}
            {(p === "ready" || p === "booting" || p === "suspended") && (
              <StopButton env={env} sandbox={sb} />
            )}
            {(p === "ready" || p === "stopped" || p === "suspended") && (
              <Button variant="outline" onClick={() => setResizing(true)}>
                <CpuIcon />
                Resources
              </Button>
            )}
          </div>
        </PopoverContent>
      </Popover>
      <ResizeSandboxDialog
        env={env}
        sandbox={sb}
        open={resizing}
        onOpenChange={setResizing}
      />
    </>
  )
}

function Fact({ label, children }: { label: string; children: ReactNode }) {
  return (
    <>
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="flex min-w-0 flex-wrap items-center gap-1">{children}</dd>
    </>
  )
}

/** The listening ports, each opening its preview in a tab of its own. */
function Ports({ env, sandbox: sb }: { env: string; sandbox: Sandbox }) {
  const ports = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes/{id}/ports",
    { params: { path: { env, id: sb.id } } },
    { refetchInterval: 3000 }
  )
  const list = ports.data ?? []
  if (list.length === 0)
    return <span className="text-muted-foreground">None listening</span>
  return (
    <>
      {list.map((p) => (
        <span key={p.port} className="inline-flex items-center">
          <Button
            variant="ghost"
            size="xs"
            className="font-mono"
            title={`Open :${p.port}${onStudioMachine ? "" : " (on the machine running Studio)"}`}
            onClick={() => void openPreviewTab(env, sb.id, p.port)}
          >
            :{p.port}
            <ArrowSquareOutIcon />
          </Button>
          <Button
            variant="ghost"
            size="icon-xs"
            title={`Open fresh: ${freshPreviewHint.toLowerCase()}`}
            aria-label={`Open :${p.port} fresh`}
            onClick={() => void openPreviewTab(env, sb.id, p.port, true)}
          >
            <BroomIcon />
          </Button>
        </span>
      ))}
    </>
  )
}

/** Saves a checkpoint of the sandbox as it is now, after naming it. */
export function CheckpointButton({
  env,
  sandbox: sb,
}: {
  env: string
  sandbox: Sandbox
}) {
  const [saving, setSaving] = useState(false)
  const checkpoints = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes/{id}/checkpoints",
    { params: { path: { env, id: sb.id } } },
    { enabled: saving }
  )
  const p = phase(sb)
  if (p !== "ready" && p !== "stopped") return null
  return (
    <>
      <Button
        variant="outline"
        size="sm"
        title="Save the sandbox's disks now; restore it from ⋯ › History and checkpoints"
        onClick={() => setSaving(true)}
      >
        <FloppyDiskIcon />
        <span className="max-sm:hidden">Checkpoint</span>
      </Button>
      {saving && (
        <SaveCheckpointDialog
          environmentId={env}
          sandboxId={sb.id}
          taken={(checkpoints.data ?? []).map((c) => c.name)}
          onClose={() => setSaving(false)}
        />
      )}
    </>
  )
}

/** Where the sandbox came from and the checkpoints it can go back to, in a sheet. */
export function HistorySheet({
  env,
  sandbox: sb,
  open,
  onOpenChange,
}: {
  env: string
  sandbox: Sandbox
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const building = sb.status === "creating" || sb.status === "failed"
  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent className="data-[side=right]:w-full data-[side=right]:sm:max-w-xl">
        <SheetHeader>
          <SheetTitle>History and checkpoints</SheetTitle>
          <SheetDescription>
            Where <span className="font-mono">{sb.name}</span> came from, and
            the points it can go back to.
          </SheetDescription>
        </SheetHeader>
        <ScrollArea className="min-h-0 flex-1" viewportClassName="px-4 pb-4">
          <div className="grid gap-6 text-[0.8125rem]">
            <LineageTimeline environmentId={env} sandboxId={sb.id} />
            {!building && <CheckpointsSection env={env} sandbox={sb} />}
          </div>
        </ScrollArea>
      </SheetContent>
    </Sheet>
  )
}
