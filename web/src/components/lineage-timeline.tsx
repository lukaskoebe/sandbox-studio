import { useState, type ReactNode } from "react"
import { Link } from "@tanstack/react-router"
import { useQueryClient, type QueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  ArrowCounterClockwiseIcon,
  FloppyDiskIcon,
  GitForkIcon,
  PencilSimpleIcon,
  TrashIcon,
} from "@phosphor-icons/react"
import { suggestedName } from "@/components/checkpoints-section"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Field, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Spinner } from "@/components/ui/spinner"
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"
import { $api, errorMessage } from "@/lib/api/client"
import {
  buildTimeline,
  shortTemplate,
  statusTone,
  summarize,
  templateLabel,
  toneDot,
  type LineageCheckpoint,
  type LineageNode,
  type Step,
} from "@/lib/lineage"
import { cn } from "@/lib/utils"

// Pixels. A point is a column: a label row, a dot row centred on LINE_Y, then the generation
// and template rows below the line. Rows have fixed heights, so the line lines up with the dots.
const STEP = 136 // from one point to the next
const COLUMN = 120 // a point's width; labels truncate to it
const PAD = 8
const LABEL_H = 20
const LINE_Y = 34
const CANVAS_H = 108

/** What a checkpoint action or the Save button asks for. */
type Action =
  | { kind: "save" }
  | { kind: "rename"; checkpoint: LineageCheckpoint }
  | { kind: "restore"; checkpoint: LineageCheckpoint }
  | { kind: "delete"; checkpoint: LineageCheckpoint }

/**
 * A sandbox's history as a line: one point per event, oldest first, evenly spaced. A
 * generation starts at its create, rebase or restore. Checkpoints are nodes with a menu, and
 * the line ends at the live sandbox, which can be saved as a checkpoint.
 */
export function LineageTimeline({
  environmentId,
  sandboxId,
}: {
  environmentId: string
  sandboxId: string
}) {
  const path = { params: { path: { env: environmentId, id: sandboxId } } }
  const lineage = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes/{id}/lineage",
    path
  )
  const sandbox = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes/{id}",
    path
  )
  const [action, setAction] = useState<Action | null>(null)

  const data = lineage.data
  if (!data) {
    return lineage.isError ? (
      <p className="text-xs text-destructive">{errorMessage(lineage.error)}</p>
    ) : (
      <div className="grid min-h-24 place-items-center">
        <Spinner />
      </div>
    )
  }
  const timeline = buildTimeline(data)
  if (timeline.steps.length === 0) {
    return <p className="text-xs text-muted-foreground">No history yet</p>
  }

  const status = sandbox.data?.status
  const canSave = status === "running" || status === "stopped"
  const checkpoints = data.checkpoints ?? []
  // The live sandbox is the point after the last step.
  const count = timeline.steps.length + 1
  const x = (index: number) => PAD + COLUMN / 2 + index * STEP
  const width = x(count - 1) + COLUMN / 2 + PAD
  const now = `Now: generation ${data.generation}, ${templateLabel(data.templateId, data.templateName)}${status ? `, ${status}` : ""}`
  const close = () => setAction(null)

  return (
    <div className="min-w-0">
      <div className="min-w-0 overflow-x-auto">
        <div className="relative" style={{ width, height: CANVAS_H }}>
          <svg
            role="img"
            aria-label="Sandbox history"
            width={width}
            height={CANVAS_H}
            className="absolute inset-0 text-muted-foreground/40"
          >
            <line
              x1={x(0)}
              x2={x(count - 1)}
              y1={LINE_Y}
              y2={LINE_Y}
              stroke="currentColor"
              strokeWidth={2}
            />
            {timeline.segments.map((segment) => (
              <line
                key={segment.start}
                x1={x(segment.start)}
                x2={x(segment.start)}
                y1={LINE_Y - 6}
                y2={LINE_Y + 6}
                stroke="currentColor"
                strokeWidth={2}
              />
            ))}
            {timeline.steps.map((step, index) =>
              step.kind === "fork" ||
              (step.kind === "origin" && step.parent) ? (
                <line
                  key={step.id}
                  x1={x(index)}
                  x2={x(index)}
                  y1={LABEL_H}
                  y2={LINE_Y - 5}
                  stroke="currentColor"
                  strokeWidth={1.5}
                />
              ) : null
            )}
          </svg>
          {timeline.steps.map((step, index) => (
            <StepMarker
              key={step.id}
              step={step}
              left={x(index)}
              environmentId={environmentId}
              restoreSupported={data.checkpointRestoreSupported}
              status={status}
              onAction={setAction}
            />
          ))}
          <Marker
            left={x(count - 1)}
            title={now}
            label={<span className="font-medium">Now</span>}
            dot={
              <span
                className={cn(
                  "size-3 rounded-full",
                  toneDot[statusTone(status ?? "")]
                )}
              />
            }
            meta={`gen ${data.generation}${status ? ` · ${status}` : ""}`}
            template={
              <TemplateName
                templateId={data.templateId}
                name={data.templateName}
              />
            }
            action={
              <Button
                size="sm"
                variant="outline"
                disabled={!canSave}
                title={
                  canSave
                    ? undefined
                    : "Save checkpoints while the sandbox is running or stopped"
                }
                onClick={() => setAction({ kind: "save" })}
              >
                <FloppyDiskIcon />
                Save checkpoint
              </Button>
            }
          />
        </div>
      </div>
      <ol className="sr-only">
        {timeline.steps.map((step) => (
          <li key={step.id}>{summarize(step)}</li>
        ))}
        <li>{now}</li>
      </ol>

      {action?.kind === "save" && (
        <SaveCheckpointDialog
          environmentId={environmentId}
          sandboxId={sandboxId}
          taken={checkpoints.map((checkpoint) => checkpoint.name)}
          onClose={close}
        />
      )}
      {action?.kind === "rename" && (
        <RenameCheckpointDialog
          environmentId={environmentId}
          sandboxId={sandboxId}
          checkpoint={action.checkpoint}
          onClose={close}
        />
      )}
      {action?.kind === "restore" && (
        <RestoreCheckpointDialog
          environmentId={environmentId}
          sandboxId={sandboxId}
          checkpoint={action.checkpoint}
          onClose={close}
        />
      )}
      {action?.kind === "delete" && (
        <DeleteCheckpointDialog
          environmentId={environmentId}
          sandboxId={sandboxId}
          checkpoint={action.checkpoint}
          onClose={close}
        />
      )}
    </div>
  )
}

/** A template's name under a point, with its ID as the tooltip. */
function TemplateName({
  templateId,
  name,
}: {
  templateId?: string
  name?: string
}) {
  return (
    <span title={templateId || undefined}>
      {templateLabel(templateId, name)}
    </span>
  )
}

/** A point's column: its label above the line, its dot on it, and what sits below. */
function Marker({
  left,
  title,
  label,
  dot,
  meta,
  template,
  action,
}: {
  left: number
  title: string
  label: ReactNode
  dot: ReactNode
  meta: ReactNode
  template?: ReactNode
  action?: ReactNode
}) {
  return (
    <div
      className="absolute top-0 flex flex-col"
      style={{ left: left - COLUMN / 2, width: COLUMN, height: CANVAS_H }}
    >
      <div
        className="h-5 w-full truncate text-center text-xs leading-5"
        title={title}
      >
        {label}
      </div>
      <div className="flex h-7 items-center justify-center">{dot}</div>
      <div className="h-4 w-full truncate text-center text-[0.625rem] leading-4 text-muted-foreground">
        {meta}
      </div>
      {template && (
        <div className="h-4.5 w-full truncate text-center text-xs leading-[18px]">
          {template}
        </div>
      )}
      {action && (
        <div className="flex h-6 items-center justify-center">{action}</div>
      )}
    </div>
  )
}

/** The marker for one step: how it reads on the line, and what its dot does. */
function StepMarker({
  step,
  left,
  environmentId,
  restoreSupported,
  status,
  onAction,
}: {
  step: Step
  left: number
  environmentId: string
  restoreSupported: boolean
  status?: string
  onAction: (action: Action) => void
}) {
  const title = summarize(step)
  const generation = `gen ${step.generation}`
  switch (step.kind) {
    case "origin":
      return (
        <Marker
          left={left}
          title={title}
          label={
            step.parent ? (
              <SandboxLink
                environmentId={environmentId}
                node={step.parent}
                prefix="forked from "
              />
            ) : (
              <span className="text-muted-foreground">created</span>
            )
          }
          dot={<span className="size-2.5 rounded-full bg-muted-foreground" />}
          meta={generation}
          template={
            <TemplateName
              templateId={step.templateId}
              name={step.templateName}
            />
          }
        />
      )
    case "rebase":
      return (
        <Marker
          left={left}
          title={title}
          label={`${shortTemplate(step.fromTemplateId, step.fromTemplateName)} → ${shortTemplate(step.templateId, step.templateName)}`}
          dot={<span className="size-2 rotate-45 bg-muted-foreground" />}
          meta={generation}
          template={
            <TemplateName
              templateId={step.templateId}
              name={step.templateName}
            />
          }
        />
      )
    case "restore":
      return (
        <Marker
          left={left}
          title={title}
          label={step.name}
          dot={
            <span className="grid size-4 place-items-center rounded-full border bg-background text-muted-foreground">
              <ArrowCounterClockwiseIcon className="size-2.5" />
            </span>
          }
          meta={generation}
          template={
            <TemplateName
              templateId={step.templateId}
              name={step.templateName}
            />
          }
        />
      )
    case "checkpoint":
      if (!step.checkpoint) {
        return (
          <Marker
            left={left}
            title={title}
            label={<span className="text-muted-foreground/70">deleted</span>}
            dot={
              <span className="size-3 rounded-full border border-dashed border-muted-foreground/50" />
            }
            meta={generation}
          />
        )
      }
      return (
        <Marker
          left={left}
          title={title}
          label={step.name}
          dot={
            <CheckpointMenu
              checkpoint={step.checkpoint}
              restoreSupported={restoreSupported}
              status={status}
              onAction={onAction}
            />
          }
          meta={
            step.checkpoint.state === "ready"
              ? generation
              : `${generation} · ${step.checkpoint.state}`
          }
        />
      )
    case "fork":
      return (
        <Marker
          left={left}
          title={title}
          label={
            step.child ? (
              <SandboxLink environmentId={environmentId} node={step.child} />
            ) : (
              <span className="text-muted-foreground">forked</span>
            )
          }
          dot={<span className="size-2 rounded-full bg-muted-foreground" />}
          meta={step.child ? <NodeStatus node={step.child} /> : "not listed"}
        />
      )
  }
}

/** A sandbox's name, linked to its page. A deleted sandbox is muted and has no link. */
function SandboxLink({
  environmentId,
  node,
  prefix,
}: {
  environmentId: string
  node: LineageNode
  prefix?: string
}) {
  if (node.deleted) {
    return (
      <>
        {prefix}
        <span className="text-muted-foreground line-through">{node.name}</span>
      </>
    )
  }
  return (
    <>
      {prefix}
      <Link
        to="/e/$env/sandboxes/$id"
        params={{ env: environmentId, id: node.id }}
        className="underline-offset-2 hover:underline"
      >
        {node.name}
      </Link>
    </>
  )
}

/** A sandbox's status, with its tone. */
function NodeStatus({ node }: { node: LineageNode }) {
  return (
    <span className="inline-flex items-center gap-1">
      <span
        className={cn(
          "size-1.5 rounded-full",
          toneDot[statusTone(node.status)]
        )}
      />
      {node.deleted ? "deleted" : node.status}
    </span>
  )
}

/** A checkpoint's node: a button that opens the actions for that checkpoint. */
function CheckpointMenu({
  checkpoint,
  restoreSupported,
  status,
  onAction,
}: {
  checkpoint: LineageCheckpoint
  restoreSupported: boolean
  status?: string
  onAction: (action: Action) => void
}) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        aria-label={`Checkpoint ${checkpoint.name}`}
        className="grid size-6 place-items-center rounded-full outline-none focus-visible:ring-2 focus-visible:ring-ring/50"
      >
        <span className="size-3 rounded-full border-2 border-muted-foreground bg-background" />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="center">
        <MenuAction
          blocked={restoreBlock(checkpoint, restoreSupported, status)}
          onSelect={() => onAction({ kind: "restore", checkpoint })}
        >
          <ArrowCounterClockwiseIcon />
          Restore
        </MenuAction>
        <MenuAction
          blocked={
            restoreSupported
              ? "Not available yet"
              : "Waiting on microsandbox #1736"
          }
        >
          <GitForkIcon />
          Fork from here
        </MenuAction>
        <DropdownMenuItem
          onClick={() => onAction({ kind: "rename", checkpoint })}
        >
          <PencilSimpleIcon />
          Rename
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem
          variant="destructive"
          onClick={() => onAction({ kind: "delete", checkpoint })}
        >
          <TrashIcon />
          Delete
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

/**
 * A menu item that can be unavailable. It stays focusable and keeps the menu open, so its
 * tooltip says why to keyboard and pointer users alike.
 */
function MenuAction({
  blocked,
  onSelect,
  children,
}: {
  blocked?: string
  onSelect?: () => void
  children: ReactNode
}) {
  if (!blocked) {
    return <DropdownMenuItem onClick={onSelect}>{children}</DropdownMenuItem>
  }
  return (
    <Tooltip>
      <TooltipTrigger
        render={
          <DropdownMenuItem
            aria-disabled
            closeOnClick={false}
            className="opacity-50"
          />
        }
      >
        {children}
      </TooltipTrigger>
      <TooltipContent>{blocked}</TooltipContent>
    </Tooltip>
  )
}

/** Why a checkpoint cannot be restored now, or undefined when it can. */
function restoreBlock(
  checkpoint: LineageCheckpoint,
  restoreSupported: boolean,
  status?: string
): string | undefined {
  if (!restoreSupported) return "Restore waits on microsandbox #1736"
  if (checkpoint.state === "deleting") return "This checkpoint is being deleted"
  if (!checkpoint.restoreAllowed)
    return "Restore is available once this checkpoint is ready"
  if (status !== "stopped") return "Stop the sandbox first"
  return undefined
}

/** A checkpoint change moves the history, the checkpoint list and the sandbox's state. */
function refreshCheckpoints(queryClient: QueryClient) {
  return Promise.all(
    [
      "/api/environments/{env}/sandboxes/{id}/lineage",
      "/api/environments/{env}/sandboxes/{id}/checkpoints",
      "/api/environments/{env}/sandboxes/{id}",
      "/api/environments/{env}/sandboxes",
    ].map((queryKey) =>
      queryClient.invalidateQueries({ queryKey: ["get", queryKey] })
    )
  )
}

function SaveCheckpointDialog({
  environmentId,
  sandboxId,
  taken,
  onClose,
}: {
  environmentId: string
  sandboxId: string
  taken: string[]
  onClose: () => void
}) {
  const queryClient = useQueryClient()
  const [name, setName] = useState(() => suggestedName(taken))
  const save = $api.useMutation(
    "post",
    "/api/environments/{env}/sandboxes/{id}/checkpoints",
    {
      onSuccess: () => {
        toast.success("Checkpoint saved")
        onClose()
      },
      onSettled: () => refreshCheckpoints(queryClient),
      onError: (err) =>
        toast.error("Could not save checkpoint", {
          description: errorMessage(err),
        }),
    }
  )
  const trimmed = name.trim()

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !save.isPending) onClose()
      }}
    >
      <DialogContent showCloseButton={!save.isPending}>
        <form
          className="contents"
          onSubmit={(event) => {
            event.preventDefault()
            if (!trimmed || save.isPending) return
            save.mutate({
              params: { path: { env: environmentId, id: sandboxId } },
              body: { name: trimmed },
            })
          }}
        >
          <DialogHeader>
            <DialogTitle>Save checkpoint</DialogTitle>
            <DialogDescription>
              Saves the disk, not running processes. Stop applications first for
              consistent data.
            </DialogDescription>
          </DialogHeader>
          <Field>
            <FieldLabel htmlFor="lineage-checkpoint-name">Name</FieldLabel>
            <Input
              id="lineage-checkpoint-name"
              autoFocus
              maxLength={80}
              value={name}
              disabled={save.isPending}
              onChange={(event) => setName(event.target.value)}
            />
          </Field>
          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              disabled={save.isPending}
              onClick={onClose}
            >
              Cancel
            </Button>
            <Button type="submit" disabled={!trimmed || save.isPending}>
              {save.isPending && <Spinner />}
              Save
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function RenameCheckpointDialog({
  environmentId,
  sandboxId,
  checkpoint,
  onClose,
}: {
  environmentId: string
  sandboxId: string
  checkpoint: LineageCheckpoint
  onClose: () => void
}) {
  const queryClient = useQueryClient()
  const [name, setName] = useState(checkpoint.name)
  const rename = $api.useMutation(
    "put",
    "/api/environments/{env}/sandboxes/{id}/checkpoints/{checkpoint}",
    {
      onSuccess: () => {
        toast.success("Checkpoint renamed")
        onClose()
      },
      onSettled: () => refreshCheckpoints(queryClient),
      onError: (err) =>
        toast.error("Could not rename checkpoint", {
          description: errorMessage(err),
        }),
    }
  )
  const trimmed = name.trim()
  const unchanged = trimmed === checkpoint.name

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !rename.isPending) onClose()
      }}
    >
      <DialogContent showCloseButton={!rename.isPending}>
        <form
          className="contents"
          onSubmit={(event) => {
            event.preventDefault()
            if (!trimmed || unchanged || rename.isPending) return
            rename.mutate({
              params: {
                path: {
                  env: environmentId,
                  id: sandboxId,
                  checkpoint: checkpoint.id,
                },
              },
              body: { name: trimmed },
            })
          }}
        >
          <DialogHeader>
            <DialogTitle>Rename {checkpoint.name}</DialogTitle>
          </DialogHeader>
          <Field>
            <FieldLabel htmlFor="lineage-checkpoint-rename">Name</FieldLabel>
            <Input
              id="lineage-checkpoint-rename"
              autoFocus
              maxLength={80}
              value={name}
              disabled={rename.isPending}
              onChange={(event) => setName(event.target.value)}
            />
          </Field>
          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              disabled={rename.isPending}
              onClick={onClose}
            >
              Cancel
            </Button>
            <Button
              type="submit"
              disabled={!trimmed || unchanged || rename.isPending}
            >
              {rename.isPending && <Spinner />}
              Rename
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function RestoreCheckpointDialog({
  environmentId,
  sandboxId,
  checkpoint,
  onClose,
}: {
  environmentId: string
  sandboxId: string
  checkpoint: LineageCheckpoint
  onClose: () => void
}) {
  const queryClient = useQueryClient()
  const restore = $api.useMutation(
    "post",
    "/api/environments/{env}/sandboxes/{id}/checkpoints/{checkpoint}/restore",
    {
      onSuccess: () => {
        toast.success("Sandbox restored")
        onClose()
      },
      onSettled: () => refreshCheckpoints(queryClient),
      onError: (err) =>
        toast.error("Could not restore checkpoint", {
          description: errorMessage(err),
        }),
    }
  )

  return (
    <AlertDialog
      open
      onOpenChange={(open) => {
        if (!open && !restore.isPending) onClose()
      }}
    >
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Restore {checkpoint.name}?</AlertDialogTitle>
          <AlertDialogDescription>
            This replaces the sandbox&apos;s root filesystem, workspace, home
            directory, and Docker state with the saved checkpoint. Any work done
            since this checkpoint will be lost.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction
            variant="destructive"
            disabled={restore.isPending}
            onClick={() =>
              restore.mutate({
                params: {
                  path: {
                    env: environmentId,
                    id: sandboxId,
                    checkpoint: checkpoint.id,
                  },
                },
              })
            }
          >
            {restore.isPending && <Spinner />}
            Restore
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

function DeleteCheckpointDialog({
  environmentId,
  sandboxId,
  checkpoint,
  onClose,
}: {
  environmentId: string
  sandboxId: string
  checkpoint: LineageCheckpoint
  onClose: () => void
}) {
  const queryClient = useQueryClient()
  const remove = $api.useMutation(
    "delete",
    "/api/environments/{env}/sandboxes/{id}/checkpoints/{checkpoint}",
    {
      onSuccess: () => {
        toast.success("Checkpoint deleted")
        onClose()
      },
      onSettled: () => refreshCheckpoints(queryClient),
      onError: (err) =>
        toast.error("Could not delete checkpoint", {
          description: errorMessage(err),
        }),
    }
  )

  return (
    <AlertDialog
      open
      onOpenChange={(open) => {
        if (!open && !remove.isPending) onClose()
      }}
    >
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Delete {checkpoint.name}?</AlertDialogTitle>
          <AlertDialogDescription>
            This permanently deletes this disk checkpoint. The sandbox itself is
            not changed.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction
            variant="destructive"
            disabled={remove.isPending}
            onClick={() =>
              remove.mutate({
                params: {
                  path: {
                    env: environmentId,
                    id: sandboxId,
                    checkpoint: checkpoint.id,
                  },
                },
              })
            }
          >
            {remove.isPending && <Spinner />}
            Delete
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
