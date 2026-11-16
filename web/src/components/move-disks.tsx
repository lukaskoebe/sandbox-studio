import { useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { HardDrivesIcon } from "@phosphor-icons/react"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Spinner } from "@/components/ui/spinner"
import { Notice } from "@/components/notice"
import { $api, errorMessage, type Sandbox } from "@/lib/api/client"
import type { components } from "@/lib/api/schema.gen"

type DiskMove = components["schemas"]["DiskMove"]

/*
 * The microsandbox Studio uses since v0.23.0 keeps a sandbox's disks in a new layout and won't
 * start or copy one whose disks are still in the old one (oldDiskLayout). On Linux they move as the sandbox
 * starts; on a Mac only when microsandbox is told nothing from before the update still writes
 * to them. Move disks asks the person to say so, then Studio stops the sandboxes, moves every
 * one's disks and starts again the ones that ran (docs/sandboxes.md, Disk layout).
 */

/**
 * What a move of the disks would touch: every sandbox, in every environment, and the VMs Studio
 * drives itself. Asked again every half minute while there is any, as msb answers for them.
 */
function useDisksToMove(enabled = true) {
  return $api.useQuery("get", "/api/system/move-disks", undefined, {
    enabled,
    refetchInterval: (q) => (q.state.data?.sandboxes.length ? 30_000 : false),
  })
}

function names(list: DiskMove[]) {
  return list.map((d) => d.name).join(", ")
}

/** Home's and Computers' notice while any sandbox's disks are in the old layout. */
export function DisksNotice({ env }: { env: string }) {
  const [open, setOpen] = useState(false)
  const old = useDisksToMove().data?.sandboxes ?? []
  if (old.length === 0) return null
  return (
    <Notice
      aria-label="Disk layout"
      tone="waiting"
      icon={<HardDrivesIcon />}
      actions={
        <Button size="sm" variant="outline" onClick={() => setOpen(true)}>
          Move disks…
        </Button>
      }
    >
      microsandbox must move the disks of{" "}
      <span className="font-mono">{names(old)}</span> to its new layout before{" "}
      {old.length === 1 ? "it starts" : "they start"} or{" "}
      {old.length === 1 ? "is" : "are"} copied again.
      <MoveDisksDialog env={env} open={open} onOpenChange={setOpen} />
    </Notice>
  )
}

/** Move disks…, for a sandbox that won't start until its disks move. */
export function MoveDisksButton({
  env,
  sandbox,
  size,
  className,
}: {
  env: string
  sandbox: Sandbox
  size?: "sm" | "xs"
  className?: string
}) {
  const [open, setOpen] = useState(false)
  if (!sandbox.oldDiskLayout) return null
  return (
    <>
      <Button
        size={size}
        className={className}
        onClick={(e) => {
          // The button sits on cards that are links.
          e.preventDefault()
          e.stopPropagation()
          setOpen(true)
        }}
      >
        <HardDrivesIcon />
        Move disks…
      </Button>
      <MoveDisksDialog env={env} open={open} onOpenChange={setOpen} />
    </>
  )
}

/**
 * The person's word that nothing from before the update writes to the disks, then the move of
 * every sandbox's disks at once: on a Mac microsandbox moves them only while no sandbox from
 * before the update runs, so each one that does stops first.
 */
export function MoveDisksDialog({
  env,
  open,
  onOpenChange,
}: {
  env: string
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const queryClient = useQueryClient()
  const [stopped, setStopped] = useState(false)
  const [failures, setFailures] = useState<string[]>([])
  const sandboxes = useDisksToMove(open)
  const old = sandboxes.data?.sandboxes ?? []
  const envs = $api.useQuery("get", "/api/environments", undefined, {
    enabled: open && old.some((d) => d.environmentId !== env),
  })
  const envName = (id: string) =>
    envs.data?.find((e) => e.id === id)?.name ?? "another environment"
  const move = $api.useMutation("post", "/api/system/move-disks", {
    onSettled: () =>
      Promise.all([
        queryClient.invalidateQueries({
          queryKey: ["get", "/api/system/move-disks"],
        }),
        queryClient.invalidateQueries({
          queryKey: ["get", "/api/environments/{env}/sandboxes"],
        }),
        queryClient.invalidateQueries({
          queryKey: ["get", "/api/environments/{env}/sandboxes/{id}"],
        }),
      ]),
  })
  const pending = move.isPending

  async function start() {
    setFailures([])
    try {
      const { moved } = await move.mutateAsync({
        body: { writersStopped: true },
      })
      const failed = moved.filter((m) => m.error)
      const done = moved.length - failed.length
      setFailures(failed.map((m) => `${m.name}: ${m.error}`))
      if (done > 0)
        toast.success(
          done === 1
            ? `Moved the disks of ${moved.find((m) => !m.error)?.name}`
            : `Moved the disks of ${done} sandboxes`
        )
      if (failed.length === 0) onOpenChange(false)
    } catch (err) {
      setFailures([`Studio moved nothing: ${errorMessage(err)}`])
    }
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next && pending) return
        if (next) {
          setFailures([])
          setStopped(false)
        }
        onOpenChange(next)
      }}
    >
      <DialogContent showCloseButton={!pending}>
        <form
          className="contents"
          onSubmit={(event) => {
            event.preventDefault()
            if (!pending && stopped && old.length > 0) void start()
          }}
        >
          <DialogHeader>
            <DialogTitle>Move disks to the new layout</DialogTitle>
            <DialogDescription>
              The microsandbox this Studio runs keeps disks in a new layout, and
              starts or copies a sandbox only once its disks are moved. On a Mac
              it moves them only while nothing from before the update writes to
              them.
            </DialogDescription>
          </DialogHeader>

          {sandboxes.isPending ? (
            <div
              className="grid min-h-12 place-items-center"
              aria-label="Loading sandboxes"
            >
              <Spinner />
            </div>
          ) : old.length === 0 ? (
            <p className="text-muted-foreground">
              Every sandbox&apos;s disks are in the new layout already.
            </p>
          ) : (
            <ul
              aria-label="Sandboxes whose disks move"
              className="grid max-h-64 divide-y overflow-y-auto border"
            >
              {old.map((d) => (
                <li
                  key={d.id}
                  className="flex min-w-0 items-baseline gap-3 px-3 py-2"
                >
                  <span className="min-w-0 truncate">
                    <span className={d.own ? undefined : "font-mono"}>
                      {d.name}
                    </span>
                    {d.environmentId !== env && (
                      <span className="text-muted-foreground">
                        {" "}
                        in {envName(d.environmentId)}
                      </span>
                    )}
                  </span>
                  <span className="ml-auto shrink-0 text-right text-muted-foreground">
                    {afterMove(d)}
                  </span>
                </li>
              ))}
            </ul>
          )}

          <label className="flex items-start gap-2">
            <Checkbox
              checked={stopped}
              onCheckedChange={setStopped}
              disabled={pending}
              className="mt-0.5"
            />
            <span>
              No Studio or msb from before the update still runs
              <span className="block text-muted-foreground">
                Quit any other Studio, and stop sandboxes started with msb
                itself. Start nothing with them until the move is done.
              </span>
            </span>
          </label>
          <p className="text-muted-foreground">
            Studio stops the sandboxes that run, then moves every one&apos;s
            disks. The originals are kept beside them and nothing on them
            changes. Large disks can take a while.
          </p>

          {failures.length > 0 && !pending && (
            <div role="alert" className="grid gap-1 text-destructive">
              <p>Not everything went through:</p>
              <ul className="grid gap-0.5">
                {failures.map((f) => (
                  <li key={f} className="break-words">
                    {f}
                  </li>
                ))}
              </ul>
            </div>
          )}

          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              disabled={pending}
              onClick={() => onOpenChange(false)}
            >
              Cancel
            </Button>
            <Button
              type="submit"
              disabled={pending || !stopped || old.length === 0}
            >
              {pending && <Spinner />}
              {pending ? "Moving…" : "Move disks"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

/** What happens to d's VM while and after its disks move. */
function afterMove(d: DiskMove) {
  const runs = d.status === "running" || d.status === "starting"
  if (d.own) return runs ? "stops; starts when needed" : "stays stopped"
  if (runs) return "stops, then starts again"
  if (d.status === "suspended") return "stops, losing its memory"
  return "stays stopped"
}
