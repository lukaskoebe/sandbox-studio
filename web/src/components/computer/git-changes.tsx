import type { ReactNode } from "react"
import {
  ArrowDownIcon,
  ArrowUpIcon,
  FolderOpenIcon,
  GitBranchIcon,
} from "@phosphor-icons/react"
import { UnifiedDiff } from "@/components/review-feedback"
import { Button } from "@/components/ui/button"
import { ScrollArea } from "@/components/ui/scroll-area"
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

/** A change's key in the URL (`?change=`): its area and its path relative to /workspace. */
export function changeKey(repo: string, c: GitChange) {
  return `${c.area}:${repo ? `${repo}/${c.path}` : c.path}`
}

/** The change a key names, with its repository, while git still reports it. */
export function findChange(repos: GitRepo[] | null | undefined, key: string) {
  for (const r of repos ?? [])
    for (const c of r.changes ?? [])
      if (changeKey(r.path, c) === key) return { repo: r.path, change: c }
  return undefined
}

/** The first change git reports, which the tab shows while nothing is picked. */
export function firstChange(repos: GitRepo[] | null | undefined) {
  for (const r of repos ?? [])
    for (const { area } of areas) {
      const c = (r.changes ?? []).find((x) => x.area === area)
      if (c) return changeKey(r.path, c)
    }
  return undefined
}

/** A row of the Changes tab's rail, which shows its item beside the rail. */
export function RailRow({
  current,
  idle,
  onClick,
  className,
  children,
  ...props
}: {
  current: boolean
  /** Shown only because nothing is picked: on a narrow screen, where it isn't beside the rail, it isn't marked. */
  idle?: boolean
  onClick: () => void
  className?: string
  children: ReactNode
  "aria-label"?: string
}) {
  return (
    <button
      type="button"
      aria-current={current || undefined}
      onClick={onClick}
      className={cn(
        "flex min-h-7 w-full min-w-0 items-center gap-2 px-3 py-0.5 text-left outline-none hover:bg-muted/60 focus-visible:ring-2 focus-visible:ring-ring/30 aria-[current=true]:bg-muted",
        idle && "@max-2xl:aria-[current=true]:bg-transparent",
        className
      )}
      {...props}
    >
      {children}
    </button>
  )
}

/** A rail section's label, with a count at its end. */
export function RailLabel({
  children,
  count,
  level = 4,
}: {
  children: ReactNode
  count?: number
  /** A section of the rail is 3, a part of a repository 4. */
  level?: 3 | 4
}) {
  const H = level === 3 ? "h3" : "h4"
  return (
    <H className="flex items-center gap-2 px-3 pt-2 pb-1 text-caption font-medium text-muted-foreground">
      {children}
      {count !== undefined && (
        <span className="ml-auto font-mono text-meta">{count}</span>
      )}
    </H>
  )
}

/**
 * The repositories of /workspace as git sees them, read-only, as the rail lists them: each
 * one's branch and how far it is ahead of what it tracks, then its staged, unstaged and
 * untracked files, each showing its diff beside the rail. Committing stays the agent's, and
 * pushing goes through review.
 */
export function GitRail({
  status,
  open,
  idle,
  onOpen,
}: {
  status: ReturnType<typeof useGitStatus>
  /** The change shown, by its changeKey. */
  open?: string
  /** The change shown is only the first, as nothing is picked. */
  idle?: boolean
  onOpen: (key: string) => void
}) {
  const note = (text: string) => (
    <p className="px-3 py-2 text-muted-foreground">{text}</p>
  )
  if (status.isPending) return <Spinner className="mx-3 my-3" />
  if (status.isError)
    return note(`Can't read git's changes: ${errorMessage(status.error)}`)
  const repos = status.data.repos ?? []
  if (repos.length === 0)
    return note("There's no git repository in /workspace.")
  return (
    <>
      {repos.map((r) => (
        <Repo key={r.path} repo={r} open={open} idle={idle} onOpen={onOpen} />
      ))}
      {status.data.truncated &&
        note("And more repositories, too many to list here.")}
    </>
  )
}

function Repo({
  repo: r,
  open,
  idle,
  onOpen,
}: {
  repo: GitRepo
  open?: string
  idle?: boolean
  onOpen: (key: string) => void
}) {
  const changes = r.changes ?? []
  const name = r.path || "workspace"
  return (
    <section
      aria-label={`Repository ${name}`}
      className="min-w-0 border-b pb-1.5"
    >
      <h3 className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 border-b px-3 py-2">
        <span className="font-mono font-medium">{name}</span>
        <span className="inline-flex min-w-0 items-center gap-1 text-(--acc-git)">
          <GitBranchIcon className="size-3.5 shrink-0" />
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
        <p className="px-3 py-2 text-muted-foreground">
          Can't read it: {r.error}
        </p>
      )}
      {!r.error && changes.length === 0 && (
        <p className="px-3 py-2 text-muted-foreground">Nothing to commit.</p>
      )}
      {areas.map(({ area, label }) => {
        const shown = changes.filter((c) => c.area === area)
        if (shown.length === 0) return null
        return (
          <div key={area} className="min-w-0">
            <RailLabel count={shown.length}>{label}</RailLabel>
            <ul aria-label={label} className="min-w-0">
              {shown.map((c) => {
                const key = changeKey(r.path, c)
                return (
                  <li key={c.path} className="min-w-0">
                    <RailRow
                      current={open === key}
                      idle={idle}
                      onClick={() => onOpen(key)}
                    >
                      <ChangeLine change={c} />
                    </RailRow>
                  </li>
                )
              })}
            </ul>
          </div>
        )
      })}
      {r.truncated && (
        <p className="px-3 py-1 text-muted-foreground">
          And more changes, too many to list here.
        </p>
      )}
    </section>
  )
}

/** A change as a line: its status letter, its path with the folder dimmed, its counts. */
function ChangeLine({ change: c }: { change: GitChange }) {
  const status = statuses[c.status]
  const slash = c.path.lastIndexOf("/")
  return (
    <>
      <span
        className={cn("w-3 shrink-0 font-mono text-caption", status.cls)}
        title={c.status}
      >
        {status.letter}
      </span>
      {/* The folder gives way first, so the file's name stays. */}
      <span
        className="flex min-w-0 flex-1 font-mono text-caption"
        title={c.from ? `${c.from} → ${c.path}` : c.path}
      >
        <span className="min-w-0 shrink-[9999] truncate text-muted-foreground">
          {c.path.slice(0, slash + 1)}
        </span>
        <span className="min-w-0 truncate">{c.path.slice(slash + 1)}</span>
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
    </>
  )
}

/**
 * A change beside the rail: its status, path and area, Open in Files, and its diff. A change
 * git no longer reports, as once it is committed, says so.
 */
export function ChangeView({
  env,
  sandboxId,
  status,
  open,
  onOpenFile,
}: {
  env: string
  sandboxId: string
  status: ReturnType<typeof useGitStatus>
  open: string
  /** Shows a file in the Files tab, relative to /workspace. */
  onOpenFile: (path: string) => void
}) {
  const found = findChange(status.data?.repos, open)
  if (!found) {
    if (status.isPending) return <Spinner className="m-4" />
    return (
      <p className="p-4 text-xs text-muted-foreground">
        Git doesn't report this change any more: it may be committed or undone.
      </p>
    )
  }
  const { repo, change: c } = found
  const s = statuses[c.status]
  const full = repo ? `${repo}/${c.path}` : c.path
  const area = areas.find((a) => a.area === c.area)!.label.toLowerCase()
  return (
    <section
      aria-label={`The change to ${full}`}
      className="flex min-h-0 min-w-0 flex-1 flex-col text-xs"
    >
      <header className="flex min-w-0 flex-none flex-wrap items-center gap-x-2 gap-y-1 border-b px-3 py-2">
        <span className={cn("font-mono text-caption", s.cls)} title={c.status}>
          {s.letter}
        </span>
        <h3
          className="min-w-0 truncate font-mono font-medium"
          title={c.from ? `${c.from} → ${full}` : full}
        >
          {full}
        </h3>
        <span className="text-muted-foreground">{area}</span>
        {c.status !== "deleted" && (
          <Button
            variant="outline"
            size="xs"
            className="ml-auto"
            onClick={() => onOpenFile(full)}
          >
            <FolderOpenIcon />
            Open in Files
          </Button>
        )}
      </header>
      <ScrollArea className="min-h-0 flex-1" viewportClassName="p-3">
        <ChangeDiff
          key={open}
          env={env}
          sandboxId={sandboxId}
          repo={repo}
          change={c}
        />
      </ScrollArea>
    </section>
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
      <UnifiedDiff diff={diff.data.diff} layouts />
      {diff.data.truncated &&
        note("The rest of this diff is too long to show.")}
    </div>
  )
}
