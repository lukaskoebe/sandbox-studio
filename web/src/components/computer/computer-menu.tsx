import { useState } from "react"
import {
  ArrowClockwiseIcon,
  ClockCounterClockwiseIcon,
  CpuIcon,
  DotsThreeIcon,
  DownloadSimpleIcon,
  FloppyDiskIcon,
  GitForkIcon,
  GitMergeIcon,
  PauseIcon,
  PlayIcon,
  PlugsConnectedIcon,
  PlusIcon,
  TrashIcon,
} from "@phosphor-icons/react"
import { SaveCheckpointDialog } from "@/components/checkpoints-section"
import { ConnectionsPanel } from "@/components/connections-panel"
import { CreateSandboxDialog } from "@/components/create-sandbox-dialog"
import { ForkSandboxDialog } from "@/components/fork-sandbox-dialog"
import { MergeForkDialog } from "@/components/merge-fork"
import { RebaseSandboxDialog } from "@/components/rebase-sandbox-dialog"
import {
  ResizeSandboxDialog,
  sandboxSize,
  sizeLabel,
} from "@/components/resize-sandbox-dialog"
import { formatMiB, HistorySheet } from "@/components/sandbox-details"
import { useSandboxLifecycle } from "@/components/sandbox-lifecycle"
import { DeleteSandboxDialog } from "@/components/sandbox-options-menu"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { ScrollArea } from "@/components/ui/scroll-area"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"
import { Spinner } from "@/components/ui/spinner"
import {
  $api,
  type Sandbox,
  type SandboxUsage,
  type Teammate,
} from "@/lib/api/client"
import { shortTemplate } from "@/lib/lineage"
import { cn, formatAge, formatUptime } from "@/lib/utils"

type Dialog =
  | "checkpoint"
  | "history"
  | "fork"
  | "merge"
  | "rebase"
  | "resources"
  | "connections"
  | "another"
  | "delete"

/**
 * The computer's ⋯ on the screen's bezel: what it is and what it uses, then what is off the
 * everyday path: checkpoints, fork and merge, rebase, resources, its connections, export,
 * another computer, suspend and delete. Power has its own button beside it.
 */
export function ComputerMenu({
  env,
  sandbox: sb,
  owner,
}: {
  env: string
  sandbox: Sandbox
  owner?: Teammate
}) {
  const [dialog, setDialog] = useState<Dialog>()
  const [menuOpen, setMenuOpen] = useState(false)
  const { path, suspend, resume, phase: p } = useSandboxLifecycle(env, sb)
  const building = sb.status === "creating" || sb.status === "failed"
  const available = sb.status === "running" || sb.status === "stopped"
  const running = sb.status === "running"
  const usage = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes/{id}/usage",
    { params: { path: { env, id: sb.id } } },
    { enabled: running, refetchInterval: running ? 5000 : false, retry: false }
  )
  const checkpoints = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes/{id}/checkpoints",
    { params: { path: { env, id: sb.id } } },
    { enabled: !building }
  )
  const latest = [...(checkpoints.data ?? [])]
    .filter((c) => c.state === "ready")
    .sort((a, b) => b.createdAt.localeCompare(a.createdAt))
    .at(0)
  // A fork can be merged back while the sandbox it was forked from exists.
  const sandboxes = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes",
    { params: { path: { env } } },
    { enabled: !!sb.forkOf }
  )
  const source = sandboxes.data?.find((s) => s.id === sb.forkOf)
  const close = (open: boolean) => !open && setDialog(undefined)
  const facts = [
    shortTemplate(sb.templateId, sb.templateName),
    `${sandboxSize(sb).now.cpus} CPU`,
    formatMiB(sandboxSize(sb).now.memoryMiB),
    running && sb.startedAt
      ? formatUptime(sb.startedAt).replace(/^Up /, "up ")
      : undefined,
  ].filter(Boolean)

  return (
    <>
      <DropdownMenu modal={false} open={menuOpen} onOpenChange={setMenuOpen}>
        <DropdownMenuTrigger
          render={
            <Button
              variant="ghost"
              size="icon-sm"
              aria-label={`More for ${sb.name}`}
              title={
                sb.baseOutdated
                  ? `More for ${sb.name}: built on an older base`
                  : `More for ${sb.name}`
              }
              className="relative text-muted-foreground hover:text-foreground"
            />
          }
        >
          <DotsThreeIcon weight="bold" />
          {sb.baseOutdated && (
            <span className="absolute top-1 right-1 size-1.5 bg-(--st-waiting)" />
          )}
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="w-72">
          <div className="grid gap-0.5 px-2 pt-1.5 pb-2">
            <span className="flex min-w-0 items-baseline gap-2">
              <span className="truncate font-mono text-[0.8125rem] font-semibold">
                {sb.name}
              </span>
              {/* Its VM is ss-<id>-g<generation> (docs/sandboxes.md). */}
              <span
                title="The computer's ID"
                className="ml-auto shrink-0 font-mono text-[0.625rem] text-muted-foreground select-all"
              >
                {sb.id}
              </span>
            </span>
            <span className="truncate text-[0.6875rem] text-muted-foreground">
              {facts.join(" · ")}
            </span>
          </div>
          {running && usage.data && <Meters usage={usage.data} />}
          {sb.baseOutdated && !building && (
            <div className="mx-1 mb-1 grid gap-1 border border-(--st-waiting)/40 bg-(--st-waiting)/10 p-2.5">
              <span className="font-mono text-[0.71875rem] font-semibold text-(--st-waiting-fg) dark:text-(--st-waiting)">
                Built on an older base
              </span>
              <span className="text-[0.78125rem]">
                {sb.baseVersion ? `From ${sb.baseVersion}. ` : ""}
                Rebase for the newer harness versions and chat adapters, and
                Debian&apos;s security updates.
              </span>
              <Button
                size="sm"
                className="mt-1 justify-self-start"
                disabled={!available}
                onClick={() => {
                  setMenuOpen(false)
                  setDialog("rebase")
                }}
              >
                Rebase…
              </Button>
            </div>
          )}
          {!building && (
            <>
              <DropdownMenuSeparator />
              <DropdownMenuGroup>
                <DropdownMenuItem
                  disabled={p !== "ready" && p !== "stopped"}
                  onClick={() => setDialog("checkpoint")}
                >
                  <FloppyDiskIcon />
                  Checkpoint now
                </DropdownMenuItem>
                <DropdownMenuItem onClick={() => setDialog("history")}>
                  <ClockCounterClockwiseIcon />
                  Checkpoints
                  {latest && (
                    <span className="ml-auto max-w-32 truncate font-mono text-[0.6875rem] text-muted-foreground">
                      {latest.name} · {formatAge(latest.createdAt)}
                    </span>
                  )}
                </DropdownMenuItem>
                <DropdownMenuItem
                  disabled={!available}
                  title={
                    available
                      ? "Copy this computer into a new one, to try something risky"
                      : "Only a running or stopped computer can be forked"
                  }
                  onClick={() => setDialog("fork")}
                >
                  <GitForkIcon />
                  Fork
                </DropdownMenuItem>
                {source && (
                  <DropdownMenuItem
                    disabled={!running}
                    title={
                      running
                        ? `Merge what changed here into ${source.name}`
                        : "Start this computer to merge it"
                    }
                    onClick={() => setDialog("merge")}
                  >
                    <GitMergeIcon />
                    <span className="truncate">
                      Merge into{" "}
                      <span className="font-mono">{source.name}</span>
                    </span>
                  </DropdownMenuItem>
                )}
              </DropdownMenuGroup>
              <DropdownMenuSeparator />
              <DropdownMenuGroup>
                <DropdownMenuItem onClick={() => setDialog("connections")}>
                  <PlugsConnectedIcon />
                  Connections
                </DropdownMenuItem>
                <DropdownMenuItem
                  disabled={
                    p !== "ready" && p !== "stopped" && p !== "suspended"
                  }
                  onClick={() => setDialog("resources")}
                >
                  <CpuIcon />
                  Resources
                  <span className="ml-auto text-[0.6875rem] text-muted-foreground">
                    {sizeLabel(sb)}
                  </span>
                </DropdownMenuItem>
                <DropdownMenuItem
                  disabled={!available}
                  title={
                    available
                      ? "Move this computer onto the current base or another template"
                      : "Only a running or stopped computer can be rebased"
                  }
                  onClick={() => setDialog("rebase")}
                >
                  <ArrowClockwiseIcon />
                  Rebase
                </DropdownMenuItem>
                {available ? (
                  <DropdownMenuItem
                    render={
                      <a
                        href={`/api/environments/${encodeURIComponent(env)}/sandboxes/${encodeURIComponent(sb.id)}/export`}
                        download={`${sb.name}.studio-sandbox`}
                        title="Downloads the template, resources, /workspace and /home/agent; secrets, checkpoints and Docker state are left out."
                      />
                    }
                  >
                    <DownloadSimpleIcon />
                    Export
                  </DropdownMenuItem>
                ) : (
                  <DropdownMenuItem disabled>
                    <DownloadSimpleIcon />
                    Export
                  </DropdownMenuItem>
                )}
              </DropdownMenuGroup>
            </>
          )}
          <DropdownMenuSeparator />
          <DropdownMenuGroup>
            {owner && (
              <DropdownMenuItem onClick={() => setDialog("another")}>
                <PlusIcon />
                Another computer for {owner.name}
              </DropdownMenuItem>
            )}
            {!building && p === "suspended" && (
              <DropdownMenuItem
                disabled={resume.isPending}
                onClick={() => resume.mutate(path)}
              >
                {resume.isPending ? <Spinner /> : <PlayIcon />}
                Resume
              </DropdownMenuItem>
            )}
            {!building && p === "ready" && (
              <DropdownMenuItem
                disabled={suspend.isPending}
                title="Pause the computer in place; everything continues on resume"
                onClick={() => suspend.mutate(path)}
              >
                {suspend.isPending ? <Spinner /> : <PauseIcon />}
                Suspend
              </DropdownMenuItem>
            )}
            <DropdownMenuItem
              variant="destructive"
              onClick={() => setDialog("delete")}
            >
              <TrashIcon />
              Delete <span className="font-mono">{sb.name}</span>
            </DropdownMenuItem>
          </DropdownMenuGroup>
        </DropdownMenuContent>
      </DropdownMenu>
      {/* Rendered beside the menu: the menu closes when an item is chosen. */}
      {dialog === "checkpoint" && (
        <SaveCheckpointDialog
          environmentId={env}
          sandboxId={sb.id}
          taken={(checkpoints.data ?? []).map((c) => c.name)}
          onClose={() => setDialog(undefined)}
        />
      )}
      <HistorySheet
        env={env}
        sandbox={sb}
        open={dialog === "history"}
        onOpenChange={close}
      />
      <ForkSandboxDialog
        env={env}
        sandbox={sb}
        open={dialog === "fork"}
        onOpenChange={close}
      />
      {source && (
        <MergeForkDialog
          env={env}
          sandbox={sb}
          source={source}
          open={dialog === "merge"}
          onOpenChange={close}
        />
      )}
      <RebaseSandboxDialog
        env={env}
        sandbox={sb}
        open={dialog === "rebase"}
        onOpenChange={close}
      />
      <ResizeSandboxDialog
        env={env}
        sandbox={sb}
        open={dialog === "resources"}
        onOpenChange={close}
      />
      <Sheet open={dialog === "connections"} onOpenChange={close}>
        <SheetContent className="data-[side=right]:w-full data-[side=right]:sm:max-w-lg">
          <SheetHeader>
            <SheetTitle>Connections</SheetTitle>
            <SheetDescription className="sr-only">
              Where {sb.name} connects
            </SheetDescription>
          </SheetHeader>
          <ScrollArea className="min-h-0 flex-1">
            <ConnectionsPanel env={env} sandbox={sb} />
          </ScrollArea>
        </SheetContent>
      </Sheet>
      {owner && (
        <CreateSandboxDialog
          env={env}
          teammate={owner.id}
          open={dialog === "another"}
          onOpenChange={close}
        />
      )}
      <DeleteSandboxDialog
        env={env}
        sandbox={sb}
        open={dialog === "delete"}
        onOpenChange={close}
      />
    </>
  )
}

/** CPU and memory as two quiet bars, as msb sampled them over the last few seconds. */
function Meters({ usage }: { usage: SandboxUsage }) {
  const GiB = 1 << 30
  const mem = (b: number) => (b / GiB).toFixed(1).replace(/\.0$/, "")
  const rows = [
    {
      label: "CPU",
      share: usage.cpuPercent / 100,
      value: `${Math.round(usage.cpuPercent)}%`,
    },
    {
      label: "Memory",
      share: usage.memoryBytes / Math.max(usage.memoryTotalBytes, 1),
      value: `${mem(usage.memoryBytes)} / ${mem(usage.memoryTotalBytes)} GiB`,
    },
  ]
  return (
    <div className="grid gap-1.5 px-2 pb-2">
      {rows.map((r) => (
        <div
          key={r.label}
          className="grid grid-cols-[4rem_minmax(0,1fr)_5.5rem] items-center gap-2 text-[0.6875rem]"
        >
          <span className="text-muted-foreground">{r.label}</span>
          <span className="h-1 overflow-hidden rounded-full bg-muted">
            <span
              className={cn(
                "block h-full rounded-full bg-muted-foreground/60",
                r.share > 0.85 && "bg-(--st-waiting)"
              )}
              style={{ width: `${Math.min(100, Math.max(2, r.share * 100))}%` }}
            />
          </span>
          <span className="text-right font-mono tabular-nums">{r.value}</span>
        </div>
      ))}
    </div>
  )
}
