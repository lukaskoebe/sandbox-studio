import { useState } from "react"
import { useQueryClient, type QueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  ArrowCounterClockwiseIcon,
  FloppyDiskIcon,
  PlusIcon,
  TrashIcon,
} from "@phosphor-icons/react"
import { Button } from "@/components/ui/button"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Field, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { SectionHeader } from "@/components/section-header"
import { Spinner } from "@/components/ui/spinner"
import { $api, errorMessage, type Sandbox } from "@/lib/api/client"
import { formatAge } from "@/lib/utils"

/**
 * Disk checkpoints of one sandbox, on its dashboard, as a list with a save action. They work
 * while it is stopped. The history above shows the same checkpoints on the line.
 */
export function CheckpointsSection({
  env,
  sandbox,
}: {
  env: string
  sandbox: Sandbox
}) {
  const queryClient = useQueryClient()
  const [saving, setSaving] = useState(false)
  const path = { params: { path: { env, id: sandbox.id } } }
  const checkpoints = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes/{id}/checkpoints",
    path
  )
  const list = checkpoints.data ?? []

  const remove = $api.useMutation(
    "delete",
    "/api/environments/{env}/sandboxes/{id}/checkpoints/{checkpoint}",
    {
      onSettled: () => refresh(queryClient),
      onError: (err) =>
        toast.error("Could not delete checkpoint", {
          description: errorMessage(err),
        }),
    }
  )
  const restore = $api.useMutation(
    "post",
    "/api/environments/{env}/sandboxes/{id}/checkpoints/{checkpoint}/restore",
    {
      onSuccess: () => toast.success("Sandbox restored"),
      onSettled: () => refresh(queryClient),
      onError: (err) =>
        toast.error("Could not restore checkpoint", {
          description: errorMessage(err),
        }),
    }
  )

  const pending = remove.isPending || restore.isPending
  const canSave = sandbox.status === "running" || sandbox.status === "stopped"
  const canRestoreSandbox =
    sandbox.checkpointRestoreSupported && sandbox.status === "stopped"
  const hint = !sandbox.checkpointRestoreSupported
    ? "Restoring needs a newer runtime."
    : sandbox.status !== "stopped" && list.length > 0
      ? "Stop the sandbox to restore one."
      : undefined

  return (
    <section
      aria-label="Checkpoints"
      className="grid min-w-0 content-start gap-2"
    >
      <SectionHeader
        title="Checkpoints"
        icon={FloppyDiskIcon}
        count={list.length || undefined}
        actions={
          <Button
            variant="ghost"
            size="xs"
            disabled={!canSave}
            title={
              canSave
                ? undefined
                : "Save checkpoints while the sandbox is running or stopped"
            }
            onClick={() => setSaving(true)}
          >
            <PlusIcon />
            Save checkpoint
          </Button>
        }
      />
      <p className="text-xs text-muted-foreground">
        A checkpoint saves the disks, not running processes.{" "}
        {hint ?? (list.length > 1 && "Delete newer ones before older ones.")}
      </p>
      {checkpoints.isPending ? (
        <div className="grid h-(--wb-row) place-items-center">
          <Spinner />
        </div>
      ) : checkpoints.isError ? (
        <p className="text-xs text-destructive">
          {errorMessage(checkpoints.error)}
        </p>
      ) : list.length > 0 ? (
        <ul className="overflow-hidden rounded-(--radius) border bg-card">
          {list.map((checkpoint) => {
            const canRestore = canRestoreSandbox && checkpoint.state === "ready"
            return (
              <li
                key={checkpoint.id}
                className="flex h-(--wb-row) items-center gap-2 border-b px-3 text-xs last:border-b-0"
              >
                <FloppyDiskIcon className="shrink-0 text-muted-foreground" />
                <span className="min-w-0 flex-1 truncate">
                  {checkpoint.name}
                </span>
                {checkpoint.state !== "ready" && (
                  <span
                    className="shrink-0 font-mono text-[0.6875rem] text-muted-foreground"
                    title={
                      checkpoint.state === "deleting"
                        ? "Removal did not finish. Delete it again to retry."
                        : undefined
                    }
                  >
                    {checkpoint.state}
                  </span>
                )}
                <time
                  dateTime={checkpoint.createdAt}
                  title={new Date(checkpoint.createdAt).toLocaleString()}
                  className="shrink-0 font-mono text-[0.6875rem] text-muted-foreground"
                >
                  {formatAge(checkpoint.createdAt)}
                </time>
                {sandbox.checkpointRestoreSupported && (
                  <AlertDialog>
                    <AlertDialogTrigger
                      render={
                        <Button
                          variant="ghost"
                          size="xs"
                          disabled={pending || !canRestore}
                        />
                      }
                    >
                      <ArrowCounterClockwiseIcon />
                      Restore
                    </AlertDialogTrigger>
                    <AlertDialogContent>
                      <AlertDialogHeader>
                        <AlertDialogTitle>
                          Restore {checkpoint.name}?
                        </AlertDialogTitle>
                        <AlertDialogDescription>
                          This replaces the sandbox&apos;s root filesystem,
                          workspace, home directory, and Docker state with the
                          saved checkpoint. Any work done since this checkpoint
                          will be lost.
                        </AlertDialogDescription>
                      </AlertDialogHeader>
                      <AlertDialogFooter>
                        <AlertDialogCancel>Cancel</AlertDialogCancel>
                        <AlertDialogAction
                          variant="destructive"
                          disabled={pending || !canRestore}
                          onClick={() => {
                            if (pending || !canRestore) return
                            restore.mutate({
                              params: {
                                path: {
                                  env,
                                  id: sandbox.id,
                                  checkpoint: checkpoint.id,
                                },
                              },
                            })
                          }}
                        >
                          {restore.isPending && <Spinner />}
                          Restore
                        </AlertDialogAction>
                      </AlertDialogFooter>
                    </AlertDialogContent>
                  </AlertDialog>
                )}
                <AlertDialog>
                  <AlertDialogTrigger
                    render={
                      <Button
                        variant="ghost"
                        size="icon-xs"
                        title="Delete checkpoint"
                        aria-label="Delete checkpoint"
                        disabled={pending}
                      />
                    }
                  >
                    <TrashIcon />
                  </AlertDialogTrigger>
                  <AlertDialogContent>
                    <AlertDialogHeader>
                      <AlertDialogTitle>
                        Delete {checkpoint.name}?
                      </AlertDialogTitle>
                      <AlertDialogDescription>
                        This permanently deletes this disk checkpoint. The
                        sandbox itself is not changed.
                      </AlertDialogDescription>
                    </AlertDialogHeader>
                    <AlertDialogFooter>
                      <AlertDialogCancel>Cancel</AlertDialogCancel>
                      <AlertDialogAction
                        variant="destructive"
                        disabled={pending}
                        onClick={() =>
                          remove.mutate({
                            params: {
                              path: {
                                env,
                                id: sandbox.id,
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
              </li>
            )
          })}
        </ul>
      ) : (
        <p className="text-xs text-muted-foreground">No checkpoints yet.</p>
      )}
      {saving && (
        <SaveCheckpointDialog
          environmentId={env}
          sandboxId={sandbox.id}
          taken={list.map((checkpoint) => checkpoint.name)}
          onClose={() => setSaving(false)}
        />
      )}
    </section>
  )
}

/** Checkpoints show in the list, the history and the sandbox, so all three are refetched. */
function refresh(queryClient: QueryClient) {
  return Promise.all(
    [
      "/api/environments/{env}/sandboxes/{id}/checkpoints",
      "/api/environments/{env}/sandboxes/{id}/lineage",
      "/api/environments/{env}/sandboxes/{id}",
      "/api/environments/{env}/sandboxes",
    ].map((url) => queryClient.invalidateQueries({ queryKey: ["get", url] }))
  )
}

/** Names and saves a checkpoint of the sandbox as it is now. */
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
      onSettled: () => refresh(queryClient),
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
            <FieldLabel htmlFor="checkpoint-name">Name</FieldLabel>
            <Input
              id="checkpoint-name"
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

/** A checkpoint name no existing one has, from the time it is saved. */
export function suggestedName(existingNames: string[] = []) {
  const base = `Checkpoint ${new Date()
    .toISOString()
    .slice(0, 19)
    .replace("T", " ")} UTC`
  const names = new Set(existingNames)
  if (!names.has(base)) return base

  let suffix = 2
  while (names.has(`${base} (${suffix})`)) suffix++
  return `${base} (${suffix})`
}
