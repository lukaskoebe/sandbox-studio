import { useState } from "react"
import { Link, createFileRoute } from "@tanstack/react-router"
import { useQueryClient } from "@tanstack/react-query"
import {
  CheckCircleIcon,
  CopyIcon,
  GitMergeIcon,
  WarningIcon,
} from "@phosphor-icons/react"
import { toast } from "sonner"
import { KindChip } from "@/components/kind-chip"
import { mergeStateText, type Merge } from "@/components/merge-fork"
import { PageHeader } from "@/components/page-header"
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
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Spinner } from "@/components/ui/spinner"
import { $api, errorMessage, errorStatus } from "@/lib/api/client"
import type { components } from "@/lib/api/schema.gen"

type FileResult = components["schemas"]["FileResult"]

export const Route = createFileRoute("/e/$env/merges/$merge")({
  component: MergePage,
})

/** A path of a merge report as the sandbox sees it. */
function guestPath(p: string) {
  if (p.startsWith("home/")) return "/home/agent/" + p.slice(5)
  return "/" + p
}

const outcomeLabel: Record<string, string> = {
  theirs: "Taken from the fork",
  merged: "Merged",
  deleted: "Deleted, as in the fork",
  kept: "Session kept from this side",
}

const conflictLabel: Record<string, string> = {
  text: "Both changed the same lines",
  binary: "Both changed it",
  type: "File on one side, symlink on the other",
  delete: "Deleted on one side, changed on the other",
  error: "Couldn't be written",
}

/** The prompt that asks the scratch sandbox's teammate to resolve the conflicts. */
function resolvePrompt(m: Merge) {
  return (
    `This sandbox merges ${m.forkName} into ${m.sourceName}. Resolve the merge conflicts listed in ` +
    `/workspace/.studio-merge/report.json. In a text file, edit the conflict markers away. Any other ` +
    `conflict has an artifact under /workspace/.studio-merge, named in the report: the fork's version, or ` +
    `a note that the fork deleted the file. Settle it, then delete the artifact. Run the tests, then tell me what you decided.`
  )
}

/** A merge of a fork back into its source: its report, its conflicts, Accept and Abort. */
function MergePage() {
  const { env, merge: id } = Route.useParams()
  const queryClient = useQueryClient()
  const detail = $api.useQuery(
    "get",
    "/api/environments/{env}/merges/{merge}",
    { params: { path: { env, merge: id } } },
    {
      retry: (n, err) => errorStatus(err) !== 404 && n < 2,
      refetchInterval: (q) =>
        q.state.data?.state === "preparing" ? 2000 : false,
    }
  )
  const path = { params: { path: { env, merge: id } } }
  const refresh = () => {
    void queryClient.invalidateQueries({
      queryKey: ["get", "/api/environments/{env}/merges/{merge}"],
    })
    void queryClient.invalidateQueries({
      queryKey: ["get", "/api/environments/{env}/sandboxes/{id}/merges"],
    })
    void queryClient.invalidateQueries({
      queryKey: ["get", "/api/environments/{env}/sandboxes"],
    })
  }
  const check = $api.useMutation(
    "post",
    "/api/environments/{env}/merges/{merge}/check",
    { onSettled: refresh }
  )
  const accept = $api.useMutation(
    "post",
    "/api/environments/{env}/merges/{merge}/accept",
    { onSettled: refresh }
  )
  const abort = $api.useMutation(
    "post",
    "/api/environments/{env}/merges/{merge}/abort",
    { onSettled: refresh }
  )
  const [confirm, setConfirm] = useState<"accept" | "abort">()
  const m = detail.data

  return (
    <>
      <PageHeader>
        <h1 className="truncate text-sm font-medium">
          {m ? `Merge ${m.forkName} into ${m.sourceName}` : "Merge"}
        </h1>
      </PageHeader>
      <ScrollArea className="min-h-0 flex-1" viewportClassName="p-4">
        <div className="mx-auto grid max-w-3xl gap-4 text-xs">
          {detail.isPending ? (
            <Spinner className="mx-auto block" />
          ) : !m ? (
            <Empty className="border">
              <EmptyHeader>
                <EmptyMedia variant="icon">
                  <GitMergeIcon />
                </EmptyMedia>
                <EmptyTitle>No such merge</EmptyTitle>
                <EmptyDescription>
                  {errorMessage(detail.error)}
                </EmptyDescription>
              </EmptyHeader>
            </Empty>
          ) : (
            <>
              <div className="grid gap-1.5">
                <KindChip area="git" icon={GitMergeIcon} className="self-start">
                  Merge
                </KindChip>
                <h2 className="text-sm font-medium">
                  {m.forkName || "A deleted fork"} into{" "}
                  {m.sourceName || "a deleted sandbox"}
                </h2>
                <p className="text-muted-foreground">{mergeStateText(m)}</p>
              </div>

              {m.state === "preparing" && (
                <p className="flex items-center gap-2 rounded-md border px-3 py-2.5">
                  <Spinner />
                  Forking {m.sourceName} into{" "}
                  {m.scratchName || "a scratch sandbox"} and merging{" "}
                  {m.forkName}&apos;s changes there. This takes a few minutes.
                </p>
              )}
              {m.state === "failed" && (
                <p
                  role="alert"
                  className="rounded-md border border-destructive/40 px-3 py-2.5 text-destructive"
                >
                  The merge failed: {m.error}
                </p>
              )}
              {m.state === "accepted" && (
                <p
                  role="status"
                  className="flex items-center gap-2 rounded-md border px-3 py-2.5"
                >
                  <CheckCircleIcon className="size-4 shrink-0 text-emerald-600" />
                  <span>
                    Merged into{" "}
                    <Link
                      className="underline"
                      to="/e/$env/sandboxes/$id"
                      params={{ env, id: m.sourceId }}
                    >
                      {m.sourceName}
                    </Link>
                    . A checkpoint taken just before is in its history, to go
                    back to.
                  </span>
                </p>
              )}
              {m.state === "aborted" && (
                <p className="rounded-md border border-dashed px-3 py-2.5">
                  Aborted. {m.sourceName} was not changed.
                </p>
              )}

              {(m.state === "ready" ||
                m.state === "conflicts" ||
                m.state === "failed") && (
                <div className="flex flex-wrap gap-2">
                  {m.scratchId && m.scratchName && (
                    <Button
                      variant="outline"
                      size="sm"
                      render={
                        <Link
                          to="/e/$env/sandboxes/$id"
                          params={{ env, id: m.scratchId }}
                        />
                      }
                    >
                      Open {m.scratchName}
                    </Button>
                  )}
                  {m.state !== "failed" && (
                    <>
                      <Button
                        variant="outline"
                        size="sm"
                        disabled={check.isPending}
                        onClick={() => check.mutate(path)}
                      >
                        {check.isPending && <Spinner />}
                        Check again
                      </Button>
                      <Button
                        size="sm"
                        disabled={m.state !== "ready" || accept.isPending}
                        title={
                          m.state === "ready"
                            ? undefined
                            : "Resolve the conflicts first, then check again"
                        }
                        onClick={() => setConfirm("accept")}
                      >
                        {accept.isPending ? <Spinner /> : <GitMergeIcon />}
                        Accept
                      </Button>
                    </>
                  )}
                  {(m.state !== "failed" || m.scratchName) && (
                    <Button
                      variant="ghost"
                      size="sm"
                      className="text-destructive"
                      disabled={abort.isPending}
                      onClick={() => setConfirm("abort")}
                    >
                      {abort.isPending && <Spinner />}
                      {m.state === "failed"
                        ? `Delete ${m.scratchName}`
                        : "Abort"}
                    </Button>
                  )}
                </div>
              )}
              {[check, accept, abort].map(
                (mutation, i) =>
                  mutation.isError && (
                    <p key={i} role="alert" className="text-destructive">
                      {errorMessage(mutation.error)}
                    </p>
                  )
              )}

              {m.report && <MergeReport merge={m} />}
            </>
          )}
        </div>
      </ScrollArea>
      {m && (
        <AlertDialog
          open={confirm !== undefined}
          onOpenChange={(open) => !open && setConfirm(undefined)}
        >
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>
                {confirm === "accept"
                  ? `Merge into ${m.sourceName}?`
                  : `Abort the merge?`}
              </AlertDialogTitle>
              <AlertDialogDescription>
                {confirm === "accept"
                  ? `Studio checkpoints ${m.sourceName}, copies everything that changed in ${m.scratchName} into it, and deletes ${m.scratchName}. If ${m.sourceName} changed a file since the merge started, nothing is copied.`
                  : `Deletes ${m.scratchName || "the scratch sandbox"} with what was resolved in it. ${m.sourceName} stays as it is.`}
              </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel>Cancel</AlertDialogCancel>
              <AlertDialogAction
                variant={confirm === "abort" ? "destructive" : "default"}
                onClick={() => {
                  if (confirm === "accept") accept.mutate(path)
                  else abort.mutate(path)
                  setConfirm(undefined)
                }}
              >
                {confirm === "accept" ? "Merge" : "Abort"}
              </AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>
      )}
    </>
  )
}

function MergeReport({ merge: m }: { merge: Merge }) {
  const report = m.report!
  const files = report.files ?? []
  const conflicts = files.filter((f) => f.outcome === "conflict")
  const others = files.filter((f) => f.outcome !== "conflict")
  const branches = report.branches ?? []
  const installs = report.installs ?? []
  const open = m.state === "ready" || m.state === "conflicts"

  return (
    <div className="grid gap-4">
      {report.noBase && (
        <p className="flex gap-2 rounded-md border px-3 py-2.5 text-amber-700 dark:text-amber-400">
          <WarningIcon className="mt-0.5 size-3 shrink-0" />
          {m.forkName} was forked before Studio kept a record of what it was
          forked from, so nothing counts as deleted and every file both sides
          have differently is a conflict.
        </p>
      )}

      {conflicts.length > 0 && (
        <section className="grid gap-2" aria-label="Conflicts">
          <h3 className="font-medium">Conflicts</h3>
          {open && (
            <div className="grid gap-2 rounded-md border px-3 py-2.5 text-muted-foreground">
              <p>
                Resolve them in {m.scratchName}: edit the conflict markers out
                of a text file; for any other conflict, put the version you want
                in place and delete its file under /workspace/.studio-merge. Run
                the tests there if you like, then check again.
              </p>
              <p>
                Or ask the teammate in {m.scratchName} to do it.{" "}
                <Button
                  variant="link"
                  size="sm"
                  className="h-auto p-0 text-xs"
                  onClick={() => {
                    void navigator.clipboard.writeText(resolvePrompt(m))
                    toast.success("Copied the prompt")
                  }}
                >
                  <CopyIcon />
                  Copy a prompt for it
                </Button>
              </p>
            </div>
          )}
          <ul className="grid gap-1">
            {conflicts.map((f) => (
              <ConflictRow key={f.path} file={f} />
            ))}
          </ul>
        </section>
      )}

      <section className="grid gap-2" aria-label="Files">
        <h3 className="font-medium">
          Files{" "}
          <span className="font-normal text-muted-foreground">
            {others.length} changed
            {report.unchanged > 0 && `, ${report.unchanged} already the same`}
          </span>
        </h3>
        {others.length === 0 ? (
          <p className="text-muted-foreground">
            Nothing else to change from the fork.
          </p>
        ) : (
          <ul className="grid gap-0.5">
            {others.map((f) => (
              <li key={f.path} className="flex gap-3">
                <span className="w-44 shrink-0 text-muted-foreground">
                  {outcomeLabel[f.outcome] ?? f.outcome}
                </span>
                <code className="min-w-0 truncate" title={guestPath(f.path)}>
                  {guestPath(f.path)}
                </code>
              </li>
            ))}
          </ul>
        )}
      </section>

      {branches.length > 0 && (
        <section className="grid gap-2" aria-label="Branches">
          <h3 className="font-medium">Branches</h3>
          <ul className="grid gap-0.5">
            {branches.map((b) => (
              <li key={b.repo + b.branch} className="flex gap-3">
                <code
                  className="w-44 shrink-0 truncate"
                  title={guestPath(b.repo)}
                >
                  {guestPath(b.repo)}
                </code>
                {b.error ? (
                  <span className="text-destructive">
                    {b.branch}: {b.error}
                  </span>
                ) : (
                  <span>
                    {b.branch} →{" "}
                    <code>{b.ref?.replace("refs/heads/", "")}</code>
                  </span>
                )}
              </li>
            ))}
          </ul>
        </section>
      )}

      {installs.length > 0 && (
        <section className="grid gap-2" aria-label="Tool installs">
          <h3 className="font-medium">Tool installs copied</h3>
          <ul className="grid gap-0.5">
            {installs.map((p) => (
              <li key={p}>
                <code>{guestPath(p)}</code>
              </li>
            ))}
          </ul>
        </section>
      )}
    </div>
  )
}

function ConflictRow({ file: f }: { file: FileResult }) {
  return (
    <li className="grid gap-0.5 rounded-md border px-3 py-2">
      <div className="flex items-center gap-2">
        {f.resolved ? (
          <CheckCircleIcon className="size-3.5 shrink-0 text-emerald-600" />
        ) : (
          <WarningIcon className="size-3.5 shrink-0 text-amber-600" />
        )}
        <code className="min-w-0 truncate" title={guestPath(f.path)}>
          {guestPath(f.path)}
        </code>
        {f.resolved && <span className="text-muted-foreground">resolved</span>}
      </div>
      <p className="text-muted-foreground">
        {conflictLabel[f.conflict ?? ""] ?? f.conflict}
        {f.detail && f.conflict !== "text" && `: ${f.detail}`}
        {f.artifact && f.conflict !== "text" && (
          <>
            .{" "}
            {f.conflict === "delete"
              ? "Once you decide, delete "
              : "The fork's version is "}
            <code>/workspace/.studio-merge/{f.artifact}</code>
            {f.conflict !== "delete" && "; delete it once you decide"}.
          </>
        )}
      </p>
    </li>
  )
}
