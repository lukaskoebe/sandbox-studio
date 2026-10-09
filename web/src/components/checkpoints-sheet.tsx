import { type FormEvent, useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  ArrowCounterClockwiseIcon,
  FloppyDiskIcon,
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
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from "@/components/ui/empty"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
  SheetTrigger,
} from "@/components/ui/sheet"
import { Spinner } from "@/components/ui/spinner"
import { $api, errorMessage, type Sandbox } from "@/lib/api/client"

/** Disk checkpoints for one sandbox, available even while it is stopped. */
export function CheckpointsSheet({
  env,
  sandbox,
}: {
  env: string
  sandbox: Sandbox
}) {
  const [open, setOpen] = useState(false)

  return (
    <Sheet open={open} onOpenChange={setOpen}>
      <SheetTrigger render={<Button variant="ghost" size="sm" />}>
        <FloppyDiskIcon />
        Checkpoints
      </SheetTrigger>
      <SheetContent className="data-[side=right]:sm:max-w-2xl">
        <SheetHeader>
          <SheetTitle>Checkpoints</SheetTitle>
          <SheetDescription>
            Checkpoints save disks, not running processes. Stop applications
            before stopping the sandbox for consistent data.
          </SheetDescription>
        </SheetHeader>
        {open && <CheckpointPanel env={env} sandbox={sandbox} />}
      </SheetContent>
    </Sheet>
  )
}

function CheckpointPanel({ env, sandbox }: { env: string; sandbox: Sandbox }) {
  const queryClient = useQueryClient()
  const [name, setName] = useState(() => suggestedName())
  const path = { params: { path: { env, id: sandbox.id } } }
  const checkpoints = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes/{id}/checkpoints",
    path,
    { refetchInterval: 15000 }
  )
  const list = checkpoints.data ?? []

  async function invalidate() {
    await Promise.all([
      queryClient.invalidateQueries({
        queryKey: ["get", "/api/environments/{env}/sandboxes/{id}/checkpoints"],
      }),
      queryClient.invalidateQueries({
        queryKey: ["get", "/api/environments/{env}/sandboxes/{id}"],
      }),
      queryClient.invalidateQueries({
        queryKey: ["get", "/api/environments/{env}/sandboxes"],
      }),
    ])
  }

  const create = $api.useMutation(
    "post",
    "/api/environments/{env}/sandboxes/{id}/checkpoints",
    {
      onSuccess: () => {
        const existingNames = [
          ...list.map((checkpoint) => checkpoint.name),
          name.trim(),
        ]
        setName(suggestedName(existingNames))
        toast.success("Checkpoint created")
      },
      onSettled: invalidate,
      onError: (err) =>
        toast.error("Could not create checkpoint", {
          description: errorMessage(err),
        }),
    }
  )
  const remove = $api.useMutation(
    "delete",
    "/api/environments/{env}/sandboxes/{id}/checkpoints/{checkpoint}",
    {
      onSettled: invalidate,
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
      onSettled: invalidate,
      onError: (err) =>
        toast.error("Could not restore checkpoint", {
          description: errorMessage(err),
        }),
    }
  )

  const mutationPending =
    create.isPending || remove.isPending || restore.isPending
  const canCreateCheckpoint =
    sandbox.status === "running" || sandbox.status === "stopped"
  const canRestoreSandbox =
    sandbox.checkpointRestoreSupported && sandbox.status === "stopped"

  function createCheckpoint(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const checkpointName = name.trim()
    if (
      checkpointName.length === 0 ||
      mutationPending ||
      !canCreateCheckpoint
    ) {
      return
    }
    create.mutate({ ...path, body: { name: checkpointName } })
  }

  return (
    <div className="min-h-0 flex-1 overflow-auto px-6 pb-6">
      <form className="grid gap-2 border-b pb-4" onSubmit={createCheckpoint}>
        <Label htmlFor="checkpoint-name">Checkpoint name</Label>
        <div className="flex items-center gap-2">
          <Input
            id="checkpoint-name"
            value={name}
            onChange={(event) => setName(event.target.value)}
            disabled={mutationPending}
            maxLength={80}
            placeholder="Checkpoint name"
          />
          <Button
            type="submit"
            disabled={
              mutationPending ||
              !canCreateCheckpoint ||
              name.trim().length === 0
            }
          >
            {create.isPending ? <Spinner /> : <FloppyDiskIcon />}
            Save
          </Button>
        </div>
      </form>

      {!sandbox.checkpointRestoreSupported && (
        <p className="pt-4 text-xs text-muted-foreground">
          Restoring checkpoints is unavailable with this runtime version.
        </p>
      )}

      {!canCreateCheckpoint ? (
        <p className="pt-4 text-xs text-muted-foreground">
          Save checkpoints while the sandbox is running or stopped.
        </p>
      ) : sandbox.checkpointRestoreSupported && !canRestoreSandbox ? (
        <p className="pt-4 text-xs text-muted-foreground">
          Stop the sandbox to restore a checkpoint.
        </p>
      ) : null}

      {!checkpoints.isPending && !checkpoints.isError && list.length > 0 && (
        <p className="pt-4 text-xs text-muted-foreground">
          Delete newer checkpoints before the checkpoints they depend on.
        </p>
      )}

      {checkpoints.isPending ? (
        <div className="grid min-h-24 place-items-center">
          <Spinner />
        </div>
      ) : checkpoints.isError ? (
        <p className="pt-4 text-xs text-destructive">
          {errorMessage(checkpoints.error)}
        </p>
      ) : list.length === 0 ? (
        <Empty className="mt-4">
          <EmptyHeader>
            <EmptyTitle>No checkpoints yet</EmptyTitle>
            <EmptyDescription>
              Save a checkpoint to capture this sandbox&apos;s current disk
              state.
            </EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        <ul className="divide-y">
          {list.map((checkpoint) => {
            const canRestore = canRestoreSandbox && checkpoint.state === "ready"
            return (
              <li
                key={checkpoint.id}
                className="grid gap-3 py-4 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-center"
              >
                <div className="min-w-0">
                  <p className="truncate font-medium">{checkpoint.name}</p>
                  <p className="mt-1 text-muted-foreground">
                    <time dateTime={checkpoint.createdAt}>
                      {new Date(checkpoint.createdAt).toLocaleString()}
                    </time>
                    <span className="px-1.5">·</span>
                    <span className="capitalize">{checkpoint.state}</span>
                  </p>
                  {checkpoint.state === "deleting" ? (
                    <p className="mt-1 text-muted-foreground">
                      Removal did not finish. Select Delete to retry.
                    </p>
                  ) : canRestoreSandbox && checkpoint.state !== "ready" ? (
                    <p className="mt-1 text-muted-foreground">
                      Restore is available when this checkpoint is ready.
                    </p>
                  ) : null}
                </div>
                <div className="flex items-center gap-1">
                  {sandbox.checkpointRestoreSupported && (
                    <AlertDialog>
                      <AlertDialogTrigger
                        render={
                          <Button
                            variant="outline"
                            size="sm"
                            disabled={mutationPending || !canRestore}
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
                            workspace, and Docker state with the saved
                            checkpoint. Any work done since this checkpoint will
                            be lost.
                          </AlertDialogDescription>
                        </AlertDialogHeader>
                        <AlertDialogFooter>
                          <AlertDialogCancel>Cancel</AlertDialogCancel>
                          <AlertDialogAction
                            variant="destructive"
                            disabled={mutationPending || !canRestore}
                            onClick={() => {
                              if (mutationPending || !canRestore) return
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
                          size="icon-sm"
                          title="Delete checkpoint"
                          aria-label="Delete checkpoint"
                          disabled={mutationPending}
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
                          disabled={mutationPending}
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
                </div>
              </li>
            )
          })}
        </ul>
      )}
    </div>
  )
}

function suggestedName(existingNames: string[] = []) {
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
