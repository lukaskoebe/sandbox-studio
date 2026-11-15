import { useState } from "react"
import {
  DotsThreeIcon,
  FloppyDiskIcon,
  GitForkIcon,
  InfoIcon,
  PauseIcon,
  PlayIcon,
  TerminalIcon,
  TrashIcon,
} from "@phosphor-icons/react"
import { SaveCheckpointDialog } from "@/components/checkpoints-section"
import { ConnectionsSheet } from "@/components/connections-panel"
import { CreateSandboxDialog } from "@/components/create-sandbox-dialog"
import { BaseDialog } from "@/components/base-dialog"
import { ForkSandboxDialog } from "@/components/fork-sandbox-dialog"
import { MergeForkDialog } from "@/components/merge-fork"
import { RebaseSandboxDialog } from "@/components/rebase-sandbox-dialog"
import { ResizeDisksDialog } from "@/components/resize-disks-dialog"
import {
  ResizeSandboxDialog,
  sandboxSize,
} from "@/components/resize-sandbox-dialog"
import { formatMiB, HistorySheet } from "@/components/sandbox-details"
import { useSandboxLifecycle } from "@/components/sandbox-lifecycle"
import { DeleteSandboxDialog } from "@/components/sandbox-options-menu"
import { SandboxTeammateDialog } from "@/components/sandbox-teammate-dialog"
import type { ScreenTab } from "@/lib/computer"
import { SSHDialog } from "@/components/ssh-dialog"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Spinner } from "@/components/ui/spinner"
import { $api, type Sandbox, type Teammate } from "@/lib/api/client"
import { shortTemplate } from "@/lib/lineage"
import { formatUptime } from "@/lib/utils"

export type ComputerDialog =
  | "checkpoint"
  | "history"
  | "fork"
  | "base"
  | "merge"
  | "rebase"
  | "resources"
  | "disks"
  | "connections"
  | "ssh"
  | "teammate"
  | "another"
  | "delete"

/**
 * The computer's ⋯ on the screen's bezel: what it is, then five quick actions: checkpoint now,
 * fork, SSH, suspend or resume, and delete. The rest is on the Details tab, and power has its own
 * button beside it.
 */
export function ComputerMenu({
  env,
  sandbox: sb,
  owner,
  onDetails,
}: {
  env: string
  sandbox: Sandbox
  owner?: Teammate
  /** Opens the screen's Details tab, which has the rest. */
  onDetails: () => void
}) {
  const [dialog, setDialog] = useState<ComputerDialog>()
  const [menuOpen, setMenuOpen] = useState(false)
  const { path, suspend, resume, phase: p } = useSandboxLifecycle(env, sb)
  const building = sb.status === "creating" || sb.status === "failed"
  const available = sb.status === "running" || sb.status === "stopped"
  const running = sb.status === "running"
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
              <span className="truncate font-mono text-title font-semibold">
                {sb.name}
              </span>
              {/* Its VM is ss-<id>-g<generation> (docs/sandboxes.md). */}
              <span
                title="The computer's ID"
                className="ml-auto shrink-0 font-mono text-meta text-muted-foreground select-all"
              >
                {sb.id}
              </span>
            </span>
            <span className="truncate text-caption text-muted-foreground">
              {facts.join(" · ")}
            </span>
          </div>
          <DropdownMenuSeparator />
          <DropdownMenuGroup>
            {!building && (
              <DropdownMenuItem
                disabled={p !== "ready" && p !== "stopped"}
                onClick={() => setDialog("checkpoint")}
              >
                <FloppyDiskIcon />
                Checkpoint now
              </DropdownMenuItem>
            )}
            {!building && (
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
                Fork…
              </DropdownMenuItem>
            )}
            <DropdownMenuItem
              title="Reach this computer from ssh, scp or an editor that works over SSH"
              onClick={() => setDialog("ssh")}
            >
              <TerminalIcon />
              Connect over SSH
            </DropdownMenuItem>
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
              Delete <span className="font-mono">{sb.name}</span>…
            </DropdownMenuItem>
          </DropdownMenuGroup>
          <DropdownMenuSeparator />
          <DropdownMenuItem onClick={onDetails}>
            <InfoIcon />
            Everything else is in Details
            {sb.baseOutdated && (
              <span
                aria-label="built on an older base"
                className="ml-auto size-1.5 rounded-full bg-(--st-waiting)"
              />
            )}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
      {/* Rendered beside the menu: the menu closes when an item is chosen. */}
      <ComputerDialogs
        env={env}
        sandbox={sb}
        owner={owner}
        dialog={dialog}
        onClose={() => setDialog(undefined)}
      />
    </>
  )
}

/**
 * The dialogs and sheets the computer's ⋯ menu and its Details tab open, one at a time.
 */
export function ComputerDialogs({
  env,
  sandbox: sb,
  owner,
  dialog,
  onClose,
  tab,
}: {
  env: string
  sandbox: Sandbox
  owner?: Teammate
  dialog?: ComputerDialog
  onClose: () => void
  /** The screen tab they were opened from, which a move to another page keeps. */
  tab?: ScreenTab
}) {
  const building = sb.status === "creating" || sb.status === "failed"
  const checkpoints = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes/{id}/checkpoints",
    { params: { path: { env, id: sb.id } } },
    { enabled: !building }
  )
  const sandboxes = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes",
    { params: { path: { env } } },
    { enabled: !!sb.forkOf }
  )
  const source = sandboxes.data?.find((s) => s.id === sb.forkOf)
  const close = (open: boolean) => !open && onClose()
  return (
    <>
      {dialog === "checkpoint" && (
        <SaveCheckpointDialog
          environmentId={env}
          sandboxId={sb.id}
          taken={(checkpoints.data ?? []).map((c) => c.name)}
          onClose={onClose}
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
      {dialog === "base" && (
        <BaseDialog env={env} sandbox={sb} open onOpenChange={close} />
      )}
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
      <ResizeDisksDialog
        env={env}
        sandbox={sb}
        open={dialog === "disks"}
        onOpenChange={close}
      />
      <ConnectionsSheet
        env={env}
        sandbox={sb}
        open={dialog === "connections"}
        onOpenChange={close}
      />
      {dialog === "ssh" && <SSHDialog sandbox={sb} open onOpenChange={close} />}
      {dialog === "teammate" && (
        <SandboxTeammateDialog
          env={env}
          sandbox={sb}
          open
          onOpenChange={close}
          tab={tab}
        />
      )}
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
