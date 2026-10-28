import {
  useLayoutEffect,
  useRef,
  useState,
  useSyncExternalStore,
  type RefObject,
} from "react"
import {
  ArrowClockwiseIcon,
  AtIcon,
  DownloadSimpleIcon,
  FileIcon,
  SidebarSimpleIcon,
  UploadSimpleIcon,
} from "@phosphor-icons/react"
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
import { CodeView } from "@/components/code-view"
import { ancestors, FilesTree, GoToFile } from "@/components/chat/files-tree"
import { Button } from "@/components/ui/button"
import { Progress } from "@/components/ui/progress"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Spinner } from "@/components/ui/spinner"
import { $api, errorMessage } from "@/lib/api/client"
import type { components } from "@/lib/api/schema.gen"
import { languageName } from "@/lib/highlight"
import {
  maxUploadBytes,
  uploadWorkspaceFile,
  type UploadConflict,
} from "@/lib/workspace-upload"
import {
  canMentionIn,
  mentionInChat,
  watchMentionTargets,
  type Mention,
} from "@/lib/mentions"
import { cn, formatBytes } from "@/lib/utils"

type FileEntry = components["schemas"]["FileEntry"]
type FileContent = components["schemas"]["FileContent"]

/**
 * A browser of the sandbox's /workspace, as mock K draws it: the tree beside the file
 * shown, the path above them, and Go to file. Below 640px the tree opens over the file
 * and closes once one is picked. Folders opened in the tree stay open while Files moves about.
 * With no file open, the view beside the tree is empty and takes uploads into the folder
 * shown; files the selected request changed are marked.
 */
export function FilesPanel({
  env,
  sandboxId,
  dir,
  file,
  onOpen,
  changed,
  className,
}: {
  env: string
  sandboxId: string
  /** The folder shown, relative to /workspace; "" for /workspace. */
  dir: string
  /** The file shown, if any. */
  file?: string
  onOpen: (dir: string, file?: string) => void
  /** Paths the selected request changed. */
  changed: Set<string>
  className?: string
}) {
  const root = useRef<HTMLDivElement>(null)
  const find = useRef<HTMLInputElement>(null)
  const narrow = useNarrow(root)
  const [hidden, setHidden] = useState(treeHidden)
  const [over, setOver] = useState(false)
  const tree = narrow ? over : !hidden
  const place = file ?? dir

  // The tree opens to the place shown, and keeps what else was opened.
  const [expanded, setExpanded] = useState(() => new Set(ancestors(dir)))
  const [shown, setShown] = useState(dir)
  if (shown !== dir) {
    setShown(dir)
    setExpanded((prev) => new Set([...prev, ...ancestors(dir)]))
  }
  const toggle = (d: string, open: boolean) =>
    setExpanded((prev) => {
      const next = new Set(prev)
      if (open) next.add(d)
      else next.delete(d)
      return next
    })
  const open = (d: string, f?: string) => {
    if (f && narrow) setOver(false)
    onOpen(d, f)
  }
  const showTree = (show: boolean) => {
    if (narrow) return setOver(show)
    setHidden(!show)
    try {
      localStorage.setItem(treeKey, show ? "shown" : "hidden")
    } catch {
      // The choice lasts until the page closes.
    }
  }

  return (
    <div
      ref={root}
      className={cn("flex min-h-0 min-w-0 flex-1 flex-col gap-2", className)}
      onKeyDown={(e) => {
        const t = e.target as HTMLElement
        if (e.key === "/" && !t.closest("input, textarea, [contenteditable]")) {
          e.preventDefault()
          find.current?.focus()
        } else if (e.key === "Escape" && narrow && over) setOver(false)
      }}
    >
      <div className="flex min-w-0 items-center gap-1">
        <Button
          variant="ghost"
          size="icon-xs"
          aria-pressed={tree}
          className="aria-pressed:bg-muted"
          aria-label={tree ? "Hide the tree" : "Show the tree"}
          title={tree ? "Hide the tree" : "Show the tree"}
          onClick={() => showTree(!tree)}
        >
          <SidebarSimpleIcon />
        </Button>
        <PathCrumbs
          dir={dir}
          file={file}
          short={narrow}
          changed={changed}
          onOpen={open}
        />
        <GoToFile
          ref={find}
          env={env}
          sandboxId={sandboxId}
          onOpen={open}
          className={narrow ? "w-32" : "w-56"}
        />
      </div>
      <div className="relative flex min-h-0 flex-1 overflow-hidden rounded-md ring-1 ring-foreground/10">
        {tree && (
          <>
            {narrow && (
              <button
                type="button"
                aria-label="Close the tree"
                tabIndex={-1}
                className="absolute inset-0 z-10 bg-background/50"
                onClick={() => setOver(false)}
              />
            )}
            <ScrollArea
              className={cn(
                "border-r bg-background",
                narrow
                  ? "absolute inset-y-0 left-0 z-20 w-[min(250px,85%)] shadow-md"
                  : "w-[250px] shrink-0"
              )}
              viewportClassName="py-1"
            >
              <FilesTree
                env={env}
                sandboxId={sandboxId}
                place={place}
                expanded={expanded}
                onToggle={toggle}
                onOpen={open}
                changed={changed}
              />
            </ScrollArea>
          </>
        )}
        {file ? (
          <ScrollArea className="min-w-0 flex-1" viewportClassName="p-2.5">
            <FileView
              env={env}
              sandboxId={sandboxId}
              path={file}
              changed={changed.has(file)}
            />
          </ScrollArea>
        ) : (
          <NoFile
            env={env}
            sandboxId={sandboxId}
            dir={dir}
            tree={tree}
            onShowTree={() => showTree(true)}
          />
        )}
      </div>
    </div>
  )
}

const treeKey = "studio.files.tree"

function treeHidden() {
  try {
    return localStorage.getItem(treeKey) === "hidden"
  } catch {
    return false
  }
}

/** Whether the element is narrower than 640px, as the tree then opens over the file. */
function useNarrow(el: RefObject<HTMLElement | null>) {
  const [narrow, setNarrow] = useState(false)
  useLayoutEffect(() => {
    const node = el.current
    if (!node) return
    const measure = () => setNarrow(node.clientWidth < 640)
    measure()
    const seen = new ResizeObserver(measure)
    seen.observe(node)
    return () => seen.disconnect()
  }, [el])
  return narrow
}

/**
 * The path of the place shown, each folder a link to it and the place itself in bold. When
 * narrow, the place alone.
 */
function PathCrumbs({
  dir,
  file,
  short,
  changed,
  onOpen,
}: {
  dir: string
  file?: string
  short: boolean
  changed: Set<string>
  onOpen: (dir: string) => void
}) {
  const parts = [
    "workspace",
    ...((file ?? dir) ? (file ?? dir).split("/") : []),
  ]
  const last = parts.length - 1
  const mark = file !== undefined && changed.has(file)
  return (
    <nav
      aria-label="Path"
      title={`/${parts.join("/")}`}
      className="flex min-w-0 flex-1 items-center gap-0.5 overflow-hidden font-mono text-caption whitespace-nowrap"
    >
      {parts.map((p, i) => {
        if (short && i < last) return null
        const sep = i > 0 && !short && (
          <span className="text-muted-foreground">/</span>
        )
        if (i === last)
          return (
            <span key={i} className="flex min-w-0 items-center gap-0.5">
              {sep}
              <span
                aria-current="page"
                className="min-w-0 truncate px-1 font-semibold"
              >
                {p}
              </span>
              {mark && (
                <span
                  className="size-1.5 shrink-0 rounded-full bg-amber-500"
                  aria-label="changed"
                />
              )}
            </span>
          )
        return (
          <span key={i} className="flex min-w-0 shrink items-center gap-0.5">
            {sep}
            <button
              type="button"
              className="max-w-[16ch] truncate rounded px-1 text-muted-foreground hover:bg-muted hover:text-foreground"
              onClick={() => onOpen(parts.slice(1, i + 1).join("/"))}
            >
              {p}
            </button>
          </span>
        )
      })}
    </nav>
  )
}

/** One file of an upload, from waiting to done or failed. */
type UploadItem = {
  id: number
  name: string
  state: "waiting" | "uploading" | "done" | "failed"
  percent: number
  error?: string
}

/** A file to upload into the folder, and the policy for its name if it is taken there. */
type Planned = { file: File; conflict: UploadConflict }

function hasFiles(dt: DataTransfer) {
  return Array.from(dt.types).includes("Files")
}

/** The files of a drop, and the names of the folders in it: folders are not uploaded. */
function droppedFiles(dt: DataTransfer) {
  const files: File[] = []
  const folders: string[] = []
  for (const item of Array.from(dt.items)) {
    if (item.kind !== "file") continue
    const entry = item.webkitGetAsEntry()
    if (entry?.isDirectory) folders.push(entry.name)
    else {
      const file = item.getAsFile()
      if (file) files.push(file)
    }
  }
  return { files, folders }
}

/**
 * The uploads into one folder. Files go one at a time, each with its progress. A name the
 * folder already has is asked about first: replace it, or keep both.
 */
function useFolderUploads({
  env,
  sandboxId,
  dir,
  entries,
  onDone,
}: {
  env: string
  sandboxId: string
  dir: string
  entries: FileEntry[]
  onDone: () => void
}) {
  const [items, setItems] = useState<UploadItem[]>([])
  const [clash, setClash] = useState<{ files: File[]; taken: string[] } | null>(
    null
  )
  const nextId = useRef(0)

  const addItems = (list: Omit<UploadItem, "id">[]) => {
    const first = nextId.current
    nextId.current += list.length
    setItems((prev) => [
      ...prev,
      ...list.map((item, i) => ({ ...item, id: first + i })),
    ])
    return list.map((_, i) => first + i)
  }
  const update = (id: number, change: Partial<UploadItem>) =>
    setItems((prev) =>
      prev.map((item) => (item.id === id ? { ...item, ...change } : item))
    )

  const run = async (plan: Planned[]) => {
    const ids = addItems(
      plan.map(({ file }) => ({
        name: file.name,
        state: "waiting",
        percent: 0,
      }))
    )
    for (const [i, { file, conflict }] of plan.entries()) {
      const id = ids[i]
      update(id, { state: "uploading" })
      try {
        const saved = await uploadWorkspaceFile(
          env,
          sandboxId,
          dir,
          file,
          conflict,
          (percent) => update(id, { percent })
        )
        // Keep both saves the file under another name, which the list then shows.
        update(id, { state: "done", percent: 100, name: saved.name })
      } catch (err) {
        update(id, { state: "failed", error: errorMessage(err) })
      }
    }
    onDone()
  }

  /** Queues the files of a drop, a picker or the menu for the folder. */
  const start = (files: File[], folders: string[] = []) => {
    const refused = [
      ...folders.map((name) => ({
        name,
        error: "Folders aren't uploaded: drop the files in them instead",
      })),
      ...files
        .filter((f) => f.size > maxUploadBytes)
        .map((f) => ({
          name: f.name,
          error: `Larger than ${maxUploadBytes >> 20} MiB`,
        })),
    ]
    if (refused.length > 0) {
      addItems(
        refused.map((item) => ({ ...item, state: "failed", percent: 0 }))
      )
    }
    const accepted = files.filter((f) => f.size <= maxUploadBytes)
    if (accepted.length === 0) return
    const taken = accepted
      .map((f) => f.name)
      .filter((name) => entries.some((e) => e.name === name))
    if (taken.length > 0) setClash({ files: accepted, taken })
    else void run(accepted.map((file) => ({ file, conflict: "fail" })))
  }

  const decide = (choice: "replace" | "keep") => {
    if (!clash) return
    const plan = clash.files.map((file) => ({
      file,
      conflict: clash.taken.includes(file.name) ? choice : ("fail" as const),
    }))
    setClash(null)
    void run(plan)
  }

  return {
    items,
    clear: () => setItems([]),
    start,
    clash,
    cancelClash: () => setClash(null),
    decide,
  }
}

const uploadStates = {
  waiting: "Waiting",
  uploading: "Uploading",
  done: "Uploaded",
  failed: "Failed",
} as const

function UploadList({
  items,
  onClear,
}: {
  items: UploadItem[]
  onClear: () => void
}) {
  const busy = items.some(
    (i) => i.state === "waiting" || i.state === "uploading"
  )
  return (
    <div className="grid gap-2 rounded-md bg-muted/40 p-2 ring-1 ring-foreground/10">
      <ul className="grid gap-1.5">
        {items.map((item) => (
          <li key={item.id} className="grid min-w-0 gap-1 text-xs">
            <div className="flex min-w-0 items-center gap-2">
              <span className="min-w-0 flex-1 truncate" title={item.name}>
                {item.name}
              </span>
              <span
                className={cn(
                  "shrink-0 tabular-nums",
                  item.state === "failed"
                    ? "text-destructive"
                    : "text-muted-foreground"
                )}
              >
                {item.state === "uploading"
                  ? `${Math.round(item.percent)}%`
                  : uploadStates[item.state]}
              </span>
            </div>
            {item.state === "uploading" && <Progress value={item.percent} />}
            {item.error && <p className="text-destructive">{item.error}</p>}
          </li>
        ))}
      </ul>
      {!busy && (
        <Button
          variant="ghost"
          size="xs"
          className="justify-self-end"
          onClick={onClear}
        >
          Clear
        </Button>
      )}
    </div>
  )
}

/**
 * What Files shows with no file open: an empty view that points to the tree, which lists the
 * folder, and takes uploads into the folder, dropped on it or picked.
 */
function NoFile({
  env,
  sandboxId,
  dir,
  tree,
  onShowTree,
}: {
  env: string
  sandboxId: string
  dir: string
  /** Whether the tree is shown beside it. */
  tree: boolean
  onShowTree: () => void
}) {
  const list = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes/{id}/files",
    { params: { path: { env, id: sandboxId }, query: { path: dir } } }
  )
  const { refetch } = list
  const uploads = useFolderUploads({
    env,
    sandboxId,
    dir,
    entries: list.data?.entries ?? [],
    onDone: () => void refetch(),
  })
  const input = useRef<HTMLInputElement>(null)
  const [dragging, setDragging] = useState(false)
  const folder = dir || "the workspace"

  return (
    <div
      aria-label="No file open"
      className="relative flex min-w-0 flex-1 flex-col items-center justify-center gap-3 p-4 text-center text-xs text-muted-foreground"
      onDragOver={(e) => {
        if (!hasFiles(e.dataTransfer)) return
        e.preventDefault()
        setDragging(true)
      }}
      onDragLeave={(e) => {
        if (!e.currentTarget.contains(e.relatedTarget as Node | null))
          setDragging(false)
      }}
      onDrop={(e) => {
        if (!hasFiles(e.dataTransfer)) return
        e.preventDefault()
        setDragging(false)
        const { files, folders } = droppedFiles(e.dataTransfer)
        uploads.start(files, folders)
      }}
    >
      {dragging && (
        <div className="pointer-events-none absolute inset-2 z-10 grid place-items-center rounded-md border-2 border-dashed border-ring bg-background/90 font-medium text-foreground">
          Drop to upload into {folder}
        </div>
      )}
      <FileIcon className="size-6" />
      <p>
        {tree ? "Pick a file in the tree, or press" : "No file open. Press"}{" "}
        <kbd className="rounded border px-1 font-mono">/</kbd> to go to{" "}
        {tree ? "one" : "a file"} by name.
      </p>
      <div className="flex flex-wrap justify-center gap-2">
        {!tree && (
          <Button size="xs" variant="outline" onClick={onShowTree}>
            <SidebarSimpleIcon />
            Show the tree
          </Button>
        )}
        <Button
          size="xs"
          variant="ghost"
          title={`Upload files into ${folder}; you can also drop them here`}
          onClick={() => input.current?.click()}
        >
          <UploadSimpleIcon />
          Upload into {dir.split("/").at(-1) || "workspace"}…
        </Button>
      </div>
      <input
        ref={input}
        type="file"
        multiple
        className="hidden"
        onChange={(e) => {
          uploads.start(Array.from(e.currentTarget.files ?? []))
          e.currentTarget.value = ""
        }}
      />
      {uploads.items.length > 0 && (
        <div className="w-full max-w-sm text-left text-foreground">
          <UploadList items={uploads.items} onClear={uploads.clear} />
        </div>
      )}
      <AlertDialog
        open={uploads.clash !== null}
        onOpenChange={(open) => {
          if (!open) uploads.cancelClash()
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {uploads.clash?.taken.length === 1
                ? `${uploads.clash.taken[0]} already exists`
                : `${uploads.clash?.taken.length} files already exist`}
            </AlertDialogTitle>
            <AlertDialogDescription>
              Replace the existing file, or keep both: the new one is saved
              beside it with a number, like “name (2).ext”.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction onClick={() => uploads.decide("keep")}>
              Keep both
            </AlertDialogAction>
            <AlertDialogAction
              variant="destructive"
              onClick={() => uploads.decide("replace")}
            >
              Replace
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}

function decodeText(base64: string) {
  const bytes = Uint8Array.from(atob(base64), (c) => c.charCodeAt(0))
  return new TextDecoder().decode(bytes)
}

function FileView({
  env,
  sandboxId,
  path,
  changed,
}: {
  env: string
  sandboxId: string
  path: string
  changed: boolean
}) {
  const content = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes/{id}/files/content",
    { params: { path: { env, id: sandboxId }, query: { path } } }
  )
  const { refetch } = content
  const name = path.split("/").at(-1) ?? path
  const c = content.data
  const canMention = useSyncExternalStore(watchMentionTargets, () =>
    canMentionIn(sandboxId)
  )
  const body = useRef<HTMLDivElement>(null)
  const mention = () => {
    const m: Mention = { path }
    const lines = selectedLines(body.current)
    if (lines) [m.start, m.end] = lines[0] === lines[1] ? [lines[0], 0] : lines
    mentionInChat(sandboxId, m)
  }

  return (
    <div className="grid min-w-0 content-start gap-2">
      <div className="flex min-w-0 items-center gap-1">
        <span
          className={cn(
            "min-w-0 truncate font-mono text-xs font-semibold",
            changed && "text-amber-700 dark:text-amber-400"
          )}
          title={changed ? "Changed by the selected message" : undefined}
        >
          {name}
        </span>
        <span className="min-w-0 flex-1 truncate text-caption text-muted-foreground">
          {c &&
            [languageName(path), formatBytes(c.size)]
              .filter(Boolean)
              .join(" · ")}
        </span>
        {c?.data && (
          <Button
            variant="ghost"
            size="icon-xs"
            title="Download"
            render={
              <a
                href={`data:${c.mimeType || "application/octet-stream"};base64,${c.data}`}
                download={name}
              />
            }
          >
            <DownloadSimpleIcon />
          </Button>
        )}
        {canMention && (
          <Button
            variant="ghost"
            size="icon-xs"
            title="Mention in chat: the lines selected, or the file"
            aria-label="Mention in chat"
            // The selection stays for the lines.
            onMouseDown={(e) => e.preventDefault()}
            onClick={mention}
          >
            <AtIcon />
          </Button>
        )}
        <Button
          variant="ghost"
          size="icon-xs"
          title="Refresh"
          disabled={content.isFetching}
          onClick={() => void refetch()}
        >
          {content.isFetching ? <Spinner /> : <ArrowClockwiseIcon />}
        </Button>
      </div>
      {content.isError && (
        <p className="text-muted-foreground">
          Can't show this file: {errorMessage(content.error)}
        </p>
      )}
      <div ref={body} className="grid min-w-0">
        {c && <FileBody file={c} name={name} path={path} />}
      </div>
    </div>
  )
}

/** The first and last line of a selection within el's code, if it has one. */
function selectedLines(el: HTMLElement | null): [number, number] | undefined {
  const sel = window.getSelection()
  if (!el || !sel || sel.isCollapsed || sel.rangeCount === 0) return
  const line = (n: Node | null) => {
    const e = n instanceof Element ? n : n?.parentElement
    const l = e?.closest<HTMLElement>("[data-line]")
    return l && el.contains(l) ? Number(l.dataset.line) : undefined
  }
  const range = sel.getRangeAt(0)
  const a = line(range.startContainer)
  const b = line(range.endContainer)
  if (a === undefined || b === undefined) return
  return [Math.min(a, b), Math.max(a, b)]
}

function FileBody({
  file: c,
  name,
  path,
}: {
  file: FileContent
  name: string
  path: string
}) {
  const note = (text: string) => <p className="text-muted-foreground">{text}</p>
  if (c.unreadable)
    return note("This file is locked: not even your teammate can read it.")
  if (c.tooLarge)
    return note(
      `At ${formatBytes(c.size)} this file is too large to show here (the limit is 1 MiB).`
    )
  if (c.binary || c.data === undefined)
    return note("This kind of file can't be shown here.")
  if (c.mimeType?.startsWith("image/")) {
    return (
      <img
        src={`data:${c.mimeType};base64,${c.data}`}
        alt={name}
        className="max-w-full justify-self-start rounded-md ring-1 ring-foreground/10 [image-rendering:auto]"
      />
    )
  }
  const text = decodeText(c.data)
  if (!text) return note("This file is empty.")
  return <CodeView path={path} text={text} />
}
