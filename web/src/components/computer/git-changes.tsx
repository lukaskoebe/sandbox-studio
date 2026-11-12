import { useState } from "react"
import {
  ArrowDownIcon,
  ArrowUpIcon,
  CaretRightIcon,
  FolderOpenIcon,
  GitBranchIcon,
} from "@phosphor-icons/react"
import { UnifiedDiff } from "@/components/review-feedback"
import { Button } from "@/components/ui/button"
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible"
import { Spinner } from "@/components/ui/spinner"
import { $api, errorMessage } from "@/lib/api/client"
import type { components } from "@/lib/api/schema.gen"
import { cn } from "@/lib/utils"

export type GitRepo = components["schemas"]["GitRepo"]
type GitChange = components["schemas"]["GitChange"]

/** How often the Changes tab asks git again while it shows, and the screen while it doesn't. */
export const gitShownInterval = 10_000
export const gitHiddenInterval = 30_000

/** The query of a computer's repositories as git sees them; the screen counts their changes. */
export function useGitStatus(
  env: string,
  sandboxId: string,
  { enabled, shown }: { enabled: boolean; shown: boolean }
) {
  return $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes/{id}/git",
    { params: { path: { env, id: sandboxId } } },
    {
      enabled,
      refetchInterval: shown ? gitShownInterval : gitHiddenInterval,
      refetchOnWindowFocus: shown,
      retry: false,
    }
  )
}

/** The files git reports changed, relative to /workspace, each once. */
export function gitChangedPaths(repos?: GitRepo[] | null): string[] {
  const paths = new Set<string>()
  for (const r of repos ?? [])
    for (const c of r.changes ?? [])
      if (c.status !== "deleted")
        paths.add(r.path ? `${r.path}/${c.path}` : c.path)
  return [...paths]
}

/** The changes git reports, each file once: the count on the Changes tab. */
export function gitChangeCount(repos?: GitRepo[] | null): number {
  let n = 0
  for (const r of repos ?? [])
    n += new Set((r.changes ?? []).map((c) => c.path)).size
  return n
}

const areas: { area: GitChange["area"]; label: string }[] = [
  { area: "staged", label: "Staged" },
  { area: "unstaged", label: "Not staged" },
  { area: "untracked", label: "Untracked" },
]

const statuses: Record<GitChange["status"], { letter: string; cls: string }> = {
  added: { letter: "A", cls: "text-emerald-700 dark:text-emerald-400" },
  modified: { letter: "M", cls: "text-sky-700 dark:text-sky-400" },
  deleted: { letter: "D", cls: "text-destructive" },
  renamed: { letter: "R", cls: "text-violet-700 dark:text-violet-400" },
  conflicted: { letter: "U", cls: "text-(--st-waiting)" },
}

/**
 * The repositories of /workspace as git sees them, read-only: each one's branch and how far
 * it is ahead of what it tracks, then its staged, unstaged and untracked files, each opening
 * its diff. Committing stays the agent's, and pushing goes through review.
 */
export function GitChanges({
  env,
  sandboxId,
  status,
  onOpenFile,
}: {
  env: string
  sandboxId: string
  status: ReturnType<typeof useGitStatus>
  /** Shows a file in the Files tab, relative to /workspace. */
  onOpenFile: (path: string) => void
}) {
  const note = (text: string) => (
    <p className="py-2 text-muted-foreground">{text}</p>
  )
  if (status.isPending) return <Spinner className="my-3" />
  if (status.isError)
    return note(`Can't read git's changes: ${errorMessage(status.error)}`)
  const repos = status.data.repos ?? []
  if (repos.length === 0)
    return note("There's no git repository in /workspace.")
  return (
    <div className="grid min-w-0 gap-4">
      {repos.map((r) => (
        <Repo
          key={r.path}
          env={env}
          sandboxId={sandboxId}
          repo={r}
          onOpenFile={onOpenFile}
        />
      ))}
      {status.data.truncated &&
        note("And more repositories, too many to list here.")}
    </div>
  )
}

function Repo({
  env,
  sandboxId,
  repo: r,
  onOpenFile,
}: {
  env: string
  sandboxId: string
  repo: GitRepo
  onOpenFile: (path: string) => void
}) {
  const changes = r.changes ?? []
  const name = r.path || "workspace"
  return (
    <section aria-label={`Repository ${name}`} className="grid min-w-0 gap-2">
      <h3 className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
        <span className="font-mono font-medium">{name}</span>
        <span className="inline-flex min-w-0 items-center gap-1 text-muted-foreground">
          <GitBranchIcon className="size-3.5 shrink-0 text-(--acc-git)" />
          <span className="truncate font-mono">
            {r.branch || (r.head ? `detached at ${r.head.slice(0, 7)}` : "—")}
          </span>
        </span>
        {r.ahead > 0 && (
          <span
            className="inline-flex items-center font-mono text-meta text-muted-foreground"
            title={`${r.ahead} ${r.ahead === 1 ? "commit" : "commits"} ${r.upstream ?? "its upstream"} doesn't have`}
          >
            <ArrowUpIcon className="size-3" />
            {r.ahead}
          </span>
        )}
        {r.behind > 0 && (
          <span
            className="inline-flex items-center font-mono text-meta text-muted-foreground"
            title={`${r.behind} ${r.behind === 1 ? "commit" : "commits"} behind ${r.upstream ?? "its upstream"}`}
          >
            <ArrowDownIcon className="size-3" />
            {r.behind}
          </span>
        )}
      </h3>
      {r.error && (
        <p className="text-muted-foreground">Can't read it: {r.error}</p>
      )}
      {!r.error && changes.length === 0 && (
        <p className="text-muted-foreground">Nothing to commit.</p>
      )}
      {areas.map(({ area, label }) => {
        const shown = changes.filter((c) => c.area === area)
        if (shown.length === 0) return null
        return (
          <div key={area} className="grid min-w-0 gap-1">
            <h4 className="text-caption text-muted-foreground">
              {label}{" "}
              <span className="font-mono text-meta">{shown.length}</span>
            </h4>
            <ul aria-label={label} className="grid min-w-0 gap-1">
              {shown.map((c) => (
                <li key={c.path} className="min-w-0">
                  <ChangeRow
                    env={env}
                    sandboxId={sandboxId}
                    repo={r.path}
                    change={c}
                    onOpenFile={onOpenFile}
                  />
                </li>
              ))}
            </ul>
          </div>
        )
      })}
      {r.truncated && (
        <p className="text-muted-foreground">
          And more changes, too many to list here.
        </p>
      )}
    </section>
  )
}

function ChangeRow({
  env,
  sandboxId,
  repo,
  change: c,
  onOpenFile,
}: {
  env: string
  sandboxId: string
  repo: string
  change: GitChange
  onOpenFile: (path: string) => void
}) {
  const [open, setOpen] = useState(false)
  const status = statuses[c.status]
  const slash = c.path.lastIndexOf("/")
  const full = repo ? `${repo}/${c.path}` : c.path
  return (
    <Collapsible
      open={open}
      onOpenChange={setOpen}
      className="group/change min-w-0 rounded-lg border bg-card"
    >
      <CollapsibleTrigger className="flex w-full min-w-0 items-center gap-2 px-2.5 py-1.5 text-left outline-none focus-visible:ring-2 focus-visible:ring-ring/30">
        <span
          className={cn("w-3 shrink-0 font-mono text-caption", status.cls)}
          title={c.status}
        >
          {status.letter}
        </span>
        <span
          className="min-w-0 flex-1 truncate font-mono text-caption"
          title={c.from ? `${c.from} → ${c.path}` : c.path}
        >
          <span className="text-muted-foreground">
            {c.path.slice(0, slash + 1)}
          </span>
          {c.path.slice(slash + 1)}
        </span>
        {!c.binary && (c.added > 0 || c.removed > 0) && (
          <span className="shrink-0 font-mono text-caption">
            {c.added > 0 && (
              <span className="text-emerald-600 dark:text-emerald-400">
                +{c.added}
              </span>
            )}{" "}
            {c.removed > 0 && (
              <span className="text-destructive">−{c.removed}</span>
            )}
          </span>
        )}
        <CaretRightIcon className="size-3 shrink-0 text-muted-foreground transition-transform group-data-[open]/change:rotate-90" />
      </CollapsibleTrigger>
      <CollapsibleContent className="grid gap-2 border-t p-2">
        {open && (
          <ChangeDiff env={env} sandboxId={sandboxId} repo={repo} change={c} />
        )}
        {c.status !== "deleted" && (
          <Button
            variant="ghost"
            size="xs"
            className="justify-self-start text-muted-foreground"
            onClick={() => onOpenFile(full)}
          >
            <FolderOpenIcon />
            Open in Files
          </Button>
        )}
      </CollapsibleContent>
    </Collapsible>
  )
}

function ChangeDiff({
  env,
  sandboxId,
  repo,
  change: c,
}: {
  env: string
  sandboxId: string
  repo: string
  change: GitChange
}) {
  const diff = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes/{id}/git/diff",
    {
      params: {
        path: { env, id: sandboxId },
        query: {
          path: c.path,
          area: c.area,
          ...(repo && { repo }),
          ...(c.from && { from: c.from }),
        },
      },
    },
    // The file may change while it is open, as git status is asked again.
    { retry: false, refetchInterval: gitShownInterval }
  )
  const note = (text: string) => <p className="text-muted-foreground">{text}</p>
  if (diff.isPending) return <Spinner />
  if (diff.isError)
    return note(`Can't load the diff: ${errorMessage(diff.error)}`)
  if (c.binary) return note("This isn't a text file.")
  if (!diff.data.diff.trim()) return note("No lines changed.")
  return (
    <div className="grid min-w-0 gap-1.5">
      <UnifiedDiff diff={diff.data.diff} />
      {diff.data.truncated &&
        note("The rest of this diff is too long to show.")}
    </div>
  )
}
