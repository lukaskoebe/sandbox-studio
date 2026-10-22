import { Link, useNavigate } from "@tanstack/react-router"
import { useQueryClient } from "@tanstack/react-query"
import { GitMergeIcon } from "@phosphor-icons/react"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Spinner } from "@/components/ui/spinner"
import { $api, errorMessage, type Sandbox } from "@/lib/api/client"
import type { components } from "@/lib/api/schema.gen"

export type Merge = components["schemas"]["MergeView"]

/** A merge that can still be accepted or aborted. */
export function isOpenMerge(m: Merge) {
  return (
    m.state === "preparing" || m.state === "ready" || m.state === "conflicts"
  )
}

/**
 * Starts merging a fork back into the sandbox it was forked from, then opens the merge.
 * Opened from the fork's options menu.
 */
export function MergeForkDialog({
  env,
  sandbox,
  source,
  open,
  onOpenChange,
}: {
  env: string
  sandbox: Sandbox
  source: Sandbox
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const start = $api.useMutation(
    "post",
    "/api/environments/{env}/sandboxes/{id}/merge",
    {
      onSuccess: (merge) => {
        queryClient.invalidateQueries({
          queryKey: ["get", "/api/environments/{env}/sandboxes/{id}/merges"],
        })
        onOpenChange(false)
        navigate({
          to: "/e/$env/merges/$merge",
          params: { env, merge: merge.id },
        })
      },
    }
  )
  const running = sandbox.status === "running"

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (next) start.reset()
        if (!start.isPending) onOpenChange(next)
      }}
    >
      <DialogContent showCloseButton={!start.isPending}>
        <DialogHeader>
          <DialogTitle>
            Merge {sandbox.name} into {source.name}
          </DialogTitle>
          <DialogDescription>
            Studio forks {source.name} into a scratch sandbox and merges what
            changed in {sandbox.name} there. {source.name} isn&apos;t touched
            until you accept the result.
          </DialogDescription>
        </DialogHeader>
        <section className="rounded-md border p-3" aria-label="What is merged">
          <dl className="grid grid-cols-[7rem_minmax(0,1fr)] gap-x-4 gap-y-2">
            <dt className="text-muted-foreground">Files</dt>
            <dd>
              /workspace and /home/agent, three-way; a file both changed gets
              conflict markers
            </dd>
            <dt className="text-muted-foreground">Commits</dt>
            <dd>
              New branches arrive as fork/{sandbox.name}/&lt;branch&gt;; no
              branch is overwritten
            </dd>
            <dt className="text-muted-foreground">Left out</dt>
            <dd>Caches, ignored files, Docker state and Studio&apos;s files</dd>
          </dl>
        </section>
        {!running && (
          <p role="alert" className="text-destructive">
            Start {sandbox.name} first: its changes are read while it runs.
          </p>
        )}
        {start.isError && (
          <p role="alert" className="text-destructive">
            Could not start the merge: {errorMessage(start.error)}
          </p>
        )}
        <DialogFooter>
          <Button
            variant="outline"
            disabled={start.isPending}
            onClick={() => onOpenChange(false)}
          >
            Close
          </Button>
          <Button
            disabled={!running || start.isPending}
            onClick={() =>
              start.mutate({ params: { path: { env, id: sandbox.id } } })
            }
          >
            {start.isPending ? <Spinner /> : <GitMergeIcon />}
            Start merge
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

/** Names the open merge a sandbox takes part in, with a link to it. */
export function MergeBanner({
  env,
  sandbox,
}: {
  env: string
  sandbox: Sandbox
}) {
  const merges = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes/{id}/merges",
    { params: { path: { env, id: sandbox.id } } }
  )
  const open = merges.data?.find(isOpenMerge)
  if (!open) return null
  const role =
    open.scratchId === sandbox.id
      ? `This sandbox is where ${open.forkName || "a fork"} is merged into ${open.sourceName || "its source"}.`
      : open.sourceId === sandbox.id
        ? `${open.forkName || "A fork"} is being merged into this sandbox.`
        : `This sandbox is being merged into ${open.sourceName || "its source"}.`
  return (
    <div
      role="status"
      className="flex shrink-0 items-center gap-2 border-b bg-sky-500/5 px-3 py-1 text-xs"
    >
      <GitMergeIcon className="size-3 shrink-0" />
      <span className="min-w-0 flex-1 truncate">
        {role} {mergeStateText(open)}
      </span>
      <Button
        variant="link"
        size="sm"
        className="h-auto p-0 text-xs"
        render={
          <Link to="/e/$env/merges/$merge" params={{ env, merge: open.id }} />
        }
      >
        Review merge
      </Button>
    </div>
  )
}

export function mergeStateText(m: Merge) {
  switch (m.state) {
    case "preparing":
      return "Preparing…"
    case "ready":
      return "Ready to accept."
    case "conflicts": {
      const n = m.report?.conflicts ?? 0
      return `${n} conflict${n === 1 ? "" : "s"} left.`
    }
    case "failed":
      return "It failed."
    case "accepted":
      return "Accepted."
    case "aborted":
      return "Aborted."
  }
}
