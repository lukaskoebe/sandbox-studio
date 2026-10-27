import {
  useId,
  useImperativeHandle,
  useRef,
  useState,
  type KeyboardEvent,
} from "react"
import {
  CaretRightIcon,
  FileIcon,
  FolderIcon,
  FolderOpenIcon,
  LinkSimpleIcon,
  MagnifyingGlassIcon,
} from "@phosphor-icons/react"
import { CandidateRow, useMentionIndex } from "@/components/chat/mention-picker"
import { Spinner } from "@/components/ui/spinner"
import { $api, errorMessage } from "@/lib/api/client"
import { rank, type Candidate } from "@/lib/mentions"
import { cn, formatBytes } from "@/lib/utils"

/** The folders above a path and the path itself, as "web", "web/src", … */
export function ancestors(path: string) {
  const parts = path ? path.split("/") : []
  return parts.map((_, i) => parts.slice(0, i + 1).join("/"))
}

/** Whether a path holds a changed file, or is one. */
function holdsChange(changed: Set<string>, path: string, folder: boolean) {
  if (!folder) return changed.has(path)
  const prefix = `${path}/`
  for (const p of changed) if (p.startsWith(prefix)) return true
  return false
}

/**
 * The workspace as a tree, as mock K draws it: folders open in place and stay open while
 * Files moves about, the place shown is selected, and changed files and the folders that
 * hold them are marked. Each folder is listed once it is opened.
 */
export function FilesTree({
  env,
  sandboxId,
  place,
  expanded,
  onToggle,
  onOpen,
  changed,
}: {
  env: string
  sandboxId: string
  /** The folder or file Files shows. */
  place: string
  expanded: Set<string>
  onToggle: (dir: string, open: boolean) => void
  onOpen: (dir: string, file?: string) => void
  changed: Set<string>
}) {
  const tree = useRef<HTMLDivElement>(null)

  // The keys of a tree: ↑↓ move, → opens or steps in, ← closes or steps out, Enter opens.
  const onKeyDown = (e: KeyboardEvent) => {
    const rows = Array.from(
      tree.current?.querySelectorAll<HTMLElement>('[role="treeitem"]') ?? []
    )
    const row = (e.target as HTMLElement).closest<HTMLElement>(
      '[role="treeitem"]'
    )
    const i = row ? rows.indexOf(row) : -1
    if (!row || i < 0) return
    const path = row.dataset.path ?? ""
    const isOpen = row.getAttribute("aria-expanded")
    const focus = (r?: HTMLElement) => {
      e.preventDefault()
      r?.focus()
    }
    if (e.key === "ArrowDown") focus(rows[i + 1])
    else if (e.key === "ArrowUp") focus(rows[i - 1])
    else if (e.key === "Home") focus(rows[0])
    else if (e.key === "End") focus(rows.at(-1))
    else if (e.key === "ArrowRight") {
      e.preventDefault()
      if (isOpen === "false") onToggle(path, true)
      else if (isOpen === "true") rows[i + 1]?.focus()
    } else if (e.key === "ArrowLeft") {
      e.preventDefault()
      if (isOpen === "true") onToggle(path, false)
      else {
        const parent = path.split("/").slice(0, -1).join("/")
        rows.find((r) => r.dataset.path === parent)?.focus()
      }
    } else if (e.key === "Enter" || e.key === " ") {
      e.preventDefault()
      row.click()
    }
  }

  return (
    <div
      ref={tree}
      role="tree"
      aria-label="Workspace"
      onKeyDown={onKeyDown}
      className="grid min-w-0"
    >
      <TreeFolder
        env={env}
        sandboxId={sandboxId}
        dir=""
        level={1}
        place={place}
        expanded={expanded}
        onToggle={onToggle}
        onOpen={onOpen}
        changed={changed}
      />
    </div>
  )
}

function TreeFolder({
  env,
  sandboxId,
  dir,
  level,
  place,
  expanded,
  onToggle,
  onOpen,
  changed,
}: {
  env: string
  sandboxId: string
  dir: string
  level: number
  place: string
  expanded: Set<string>
  onToggle: (dir: string, open: boolean) => void
  onOpen: (dir: string, file?: string) => void
  changed: Set<string>
}) {
  const list = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes/{id}/files",
    { params: { path: { env, id: sandboxId }, query: { path: dir } } }
  )
  const indent = { paddingLeft: 8 + level * 14 }
  const note = (text: string, className?: string) => (
    <div
      style={indent}
      className={cn(
        "flex h-6 items-center gap-1.5 pr-2 text-caption text-muted-foreground",
        className
      )}
    >
      {text}
    </div>
  )
  if (list.isPending)
    return (
      <div
        style={indent}
        className="flex h-6 items-center gap-1.5 text-caption text-muted-foreground"
      >
        <Spinner /> Listing…
      </div>
    )
  if (list.isError)
    return note(
      `Can't list this folder: ${errorMessage(list.error)}`,
      "h-auto py-1 text-destructive"
    )
  const entries = list.data.entries ?? []
  if (entries.length === 0) return note(dir ? "Empty" : "No files yet")
  return (
    <>
      {entries.map((e, i) => {
        const path = dir ? `${dir}/${e.name}` : e.name
        const folder = e.type === "dir"
        const isOpen = folder && expanded.has(path)
        const selected = path === place
        const mark = holdsChange(changed, path, folder)
        const openable = folder || e.type === "file"
        const Icon = folder
          ? isOpen
            ? FolderOpenIcon
            : FolderIcon
          : e.type === "symlink"
            ? LinkSimpleIcon
            : FileIcon
        return (
          <div key={e.name} role="none" className="grid min-w-0">
            <div
              role="treeitem"
              data-path={path}
              aria-level={level}
              aria-expanded={folder ? isOpen : undefined}
              aria-selected={selected}
              aria-disabled={!openable || undefined}
              // One row takes the focus: the place shown, or the first.
              tabIndex={selected || (!place && level === 1 && i === 0) ? 0 : -1}
              title={
                e.type === "symlink"
                  ? "A link to another place; links aren't followed"
                  : mark
                    ? folder
                      ? "Holds files the selected message changed"
                      : "Changed by the selected message"
                    : undefined
              }
              onClick={() => {
                if (!openable) return
                if (!folder) return onOpen(dir, path)
                // A folder opens in the tree as Files moves to it; the one shown folds.
                if (selected) onToggle(path, !isOpen)
                else {
                  if (!isOpen) onToggle(path, true)
                  onOpen(path)
                }
              }}
              style={{ paddingLeft: 8 + (level - 1) * 14 }}
              className={cn(
                "flex h-6 min-w-0 cursor-default items-center gap-1.5 pr-2 font-mono text-xs whitespace-nowrap outline-none hover:bg-muted/60 focus-visible:ring-2 focus-visible:ring-ring/30 focus-visible:ring-inset",
                selected && "bg-muted shadow-[inset_2px_0_0_var(--primary)]",
                mark && "text-amber-700 dark:text-amber-400",
                !openable && "text-muted-foreground hover:bg-transparent"
              )}
            >
              {folder ? (
                <span
                  aria-hidden
                  className="grid size-3.5 shrink-0 place-items-center text-muted-foreground"
                  onClick={(ev) => {
                    ev.stopPropagation()
                    onToggle(path, !isOpen)
                  }}
                >
                  <CaretRightIcon
                    weight="bold"
                    className={cn(
                      "size-2.5 transition-transform",
                      isOpen && "rotate-90"
                    )}
                  />
                </span>
              ) : (
                <span className="size-3.5 shrink-0" />
              )}
              <Icon
                className={cn(
                  "size-3.5 shrink-0",
                  folder
                    ? "text-sky-600 dark:text-sky-400"
                    : "text-muted-foreground"
                )}
              />
              <span className="min-w-0 flex-1 truncate">{e.name}</span>
              {mark && (
                <span
                  className="size-1.5 shrink-0 rounded-full bg-amber-500"
                  aria-label="changed"
                />
              )}
              {e.type === "file" && (
                <span className="shrink-0 text-meta text-muted-foreground tabular-nums">
                  {formatBytes(e.size)}
                </span>
              )}
            </div>
            {isOpen && (
              <div role="group" className="grid min-w-0">
                <TreeFolder
                  env={env}
                  sandboxId={sandboxId}
                  dir={path}
                  level={level + 1}
                  place={place}
                  expanded={expanded}
                  onToggle={onToggle}
                  onOpen={onOpen}
                  changed={changed}
                />
              </div>
            )}
          </div>
        )
      })}
      {list.data.truncated &&
        note("Only the first 1,000 items are shown", "whitespace-nowrap")}
    </>
  )
}

/**
 * Go to file: a file or folder of the workspace by its name, ranked as the @ picker ranks
 * them. / focuses it from anywhere in Files.
 */
export function GoToFile({
  env,
  sandboxId,
  onOpen,
  ref,
  className,
}: {
  env: string
  sandboxId: string
  onOpen: (dir: string, file?: string) => void
  ref?: React.Ref<HTMLInputElement>
  className?: string
}) {
  const [query, setQuery] = useState("")
  const [focused, setFocused] = useState(false)
  const [active, setActive] = useState(0)
  const id = useId()
  const input = useRef<HTMLInputElement>(null)
  useImperativeHandle(ref, () => input.current!, [])
  const { index, all } = useMentionIndex(env, sandboxId, focused)
  const picks = all ? rank(all, query).shown : []
  const shown = focused && (index.isError || index.isPending || all)
  const at = Math.min(active, Math.max(0, picks.length - 1))

  const pick = (c: Candidate) => {
    if (c.folder) onOpen(c.path.slice(0, -1))
    else onOpen(c.dir.slice(0, -1), c.path)
    setQuery("")
    input.current?.blur()
  }

  return (
    <div className={cn("relative min-w-0", className)}>
      <div className="flex h-6 min-w-0 items-center gap-1.5 rounded-md border border-input bg-input/20 px-2 focus-within:border-ring focus-within:ring-2 focus-within:ring-ring/30 dark:bg-input/30">
        <MagnifyingGlassIcon className="size-3 shrink-0 text-muted-foreground" />
        <input
          ref={input}
          role="combobox"
          aria-label="Go to file"
          aria-expanded={!!shown}
          aria-controls={id}
          aria-activedescendant={
            shown && picks.length ? `${id}-${at}` : undefined
          }
          placeholder="Go to file"
          value={query}
          onChange={(e) => {
            setQuery(e.target.value)
            setActive(0)
          }}
          onFocus={() => setFocused(true)}
          onBlur={() => setFocused(false)}
          onKeyDown={(e) => {
            if (e.key === "ArrowDown" || e.key === "ArrowUp") {
              e.preventDefault()
              const n = picks.length
              if (n) setActive((at + (e.key === "ArrowDown" ? 1 : n - 1)) % n)
            } else if (e.key === "Enter") {
              e.preventDefault()
              if (picks[at]) pick(picks[at])
            } else if (e.key === "Escape") {
              e.preventDefault()
              e.stopPropagation()
              setQuery("")
              e.currentTarget.blur()
            }
          }}
          className="min-w-0 flex-1 bg-transparent text-xs outline-none placeholder:text-muted-foreground"
        />
        {!focused && (
          <kbd className="rounded-xs border px-1 font-mono text-meta text-muted-foreground">
            /
          </kbd>
        )}
      </div>
      {shown && (
        <div className="absolute top-full right-0 z-30 mt-1 grid w-[min(26rem,calc(100vw-2rem))] overflow-hidden rounded-lg border bg-popover text-popover-foreground shadow-md">
          {index.isPending ? (
            <div className="flex items-center gap-2 px-2.5 py-2 text-caption text-muted-foreground">
              <Spinner /> Listing the files…
            </div>
          ) : index.isError ? (
            <p className="px-2.5 py-2 text-caption text-destructive">
              Can't list the files: {errorMessage(index.error)}
            </p>
          ) : picks.length === 0 ? (
            <p className="px-2.5 py-2 text-caption text-muted-foreground">
              No file matches.
            </p>
          ) : (
            <div
              id={id}
              role="listbox"
              aria-label="Files to go to"
              className="grid py-1"
            >
              {picks.map((c, i) => (
                <CandidateRow
                  key={c.path}
                  id={`${id}-${i}`}
                  candidate={c}
                  active={i === at}
                  onPick={() => pick(c)}
                  onHover={() => setActive(i)}
                />
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  )
}
