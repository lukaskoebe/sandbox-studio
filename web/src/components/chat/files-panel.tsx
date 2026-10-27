import { useMemo, useRef, useState } from "react"
import {
  ArrowClockwiseIcon,
  ArrowLeftIcon,
  DotsThreeIcon,
  DownloadSimpleIcon,
  FileIcon,
  FolderIcon,
  LinkSimpleIcon,
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
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Progress } from "@/components/ui/progress"
import { Spinner } from "@/components/ui/spinner"
import { $api, errorMessage } from "@/lib/api/client"
import type { components } from "@/lib/api/schema.gen"
import {
  maxUploadBytes,
  uploadWorkspaceFile,
  type UploadConflict,
} from "@/lib/workspace-upload"
import { cn, formatBytes } from "@/lib/utils"

type FileEntry = components["schemas"]["FileEntry"]
type FileContent = components["schemas"]["FileContent"]

function join(dir: string, name: string) {
  return dir ? `${dir}/${name}` : name
}

/**
 * A browser of the sandbox's /workspace: one folder at a time, a viewer for text files and
 * images, and uploads into the folder shown. Files the selected request changed are marked.
 */
export function FilesPanel({
  env,
  sandboxId,
  dir,
  file,
  onOpen,
  changed,
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
}) {
  return file ? (
    <FileView
      env={env}
      sandboxId={sandboxId}
      path={file}
      onBack={() => onOpen(dir)}
    />
  ) : (
    <FolderView
      env={env}
      sandboxId={sandboxId}
      dir={dir}
      onOpen={onOpen}
      changed={changed}
    />
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

function FolderView({
  env,
  sandboxId,
  dir,
  onOpen,
  changed,
}: {
  env: string
  sandboxId: string
  dir: string
  onOpen: (dir: string, file?: string) => void
  changed: Set<string>
}) {
  const list = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes/{id}/files",
    { params: { path: { env, id: sandboxId }, query: { path: dir } } }
  )
  const { refetch } = list
  const entries = list.data?.entries ?? []
  const uploads = useFolderUploads({
    env,
    sandboxId,
    dir,
    entries,
    onDone: () => void refetch(),
  })
  const input = useRef<HTMLInputElement>(null)
  const [dragging, setDragging] = useState(false)

  // Folders that hold a changed file are marked too.
  const changedHere = useMemo(() => {
    const out = new Set<string>()
    const prefix = dir ? `${dir}/` : ""
    for (const p of changed) {
      if (p.startsWith(prefix)) out.add(p.slice(prefix.length).split("/")[0])
    }
    return out
  }, [changed, dir])

  const crumbs = dir ? dir.split("/") : []

  return (
    <div
      className="relative grid min-w-0 content-start gap-2"
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
        <div className="pointer-events-none absolute inset-0 z-10 grid place-items-center rounded-md border-2 border-dashed border-ring bg-background/90 text-xs font-medium">
          Drop to upload into {dir || "the workspace"}
        </div>
      )}
      <div className="flex min-w-0 items-center gap-1">
        <nav
          aria-label="Folder"
          className="flex min-w-0 flex-1 flex-wrap items-center gap-0.5 font-mono text-caption"
        >
          <button
            type="button"
            className="rounded px-1 hover:bg-muted"
            onClick={() => onOpen("")}
          >
            workspace
          </button>
          {crumbs.map((c, i) => (
            <span key={i} className="flex min-w-0 items-center gap-0.5">
              <span className="text-muted-foreground">/</span>
              <button
                type="button"
                className="truncate rounded px-1 hover:bg-muted"
                onClick={() => onOpen(crumbs.slice(0, i + 1).join("/"))}
              >
                {c}
              </button>
            </span>
          ))}
        </nav>
        <Button
          variant="ghost"
          size="icon-xs"
          title="Refresh"
          disabled={list.isFetching}
          onClick={() => void refetch()}
        >
          {list.isFetching ? <Spinner /> : <ArrowClockwiseIcon />}
        </Button>
        <DropdownMenu modal={false}>
          <DropdownMenuTrigger
            render={
              <Button
                variant="ghost"
                size="icon-xs"
                aria-label="Folder options"
                title="Folder options"
              />
            }
          >
            <DotsThreeIcon weight="bold" />
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem onClick={() => input.current?.click()}>
              <UploadSimpleIcon />
              Upload…
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
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
      </div>
      {uploads.items.length > 0 && (
        <UploadList items={uploads.items} onClear={uploads.clear} />
      )}
      {list.isError && (
        <p className="text-muted-foreground">
          Can't show the files: {errorMessage(list.error)}
        </p>
      )}
      {list.isSuccess && entries.length === 0 && (
        <p className="text-muted-foreground">This folder is empty.</p>
      )}
      {entries.length > 0 && (
        <ul className="grid min-w-0">
          {entries.map((e) => (
            <li key={e.name} className="min-w-0">
              <EntryRow
                entry={e}
                changed={changedHere.has(e.name)}
                onOpen={() =>
                  e.type === "dir"
                    ? onOpen(join(dir, e.name))
                    : onOpen(dir, join(dir, e.name))
                }
              />
            </li>
          ))}
        </ul>
      )}
      {list.data?.truncated && (
        <p className="text-muted-foreground">
          Only the first 1,000 items of this folder are shown.
        </p>
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

function EntryRow({
  entry: e,
  changed,
  onOpen,
}: {
  entry: FileEntry
  changed: boolean
  onOpen: () => void
}) {
  const Icon =
    e.type === "dir"
      ? FolderIcon
      : e.type === "symlink"
        ? LinkSimpleIcon
        : FileIcon
  const openable = e.type === "dir" || e.type === "file"
  return (
    <button
      type="button"
      disabled={!openable}
      onClick={onOpen}
      title={
        e.type === "symlink"
          ? "A link to another place; links aren't followed"
          : changed
            ? "Changed by the selected message"
            : undefined
      }
      className="flex w-full min-w-0 items-center gap-2 rounded-md px-1.5 py-1 text-left outline-none hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring/30 disabled:cursor-default disabled:hover:bg-transparent"
    >
      <Icon
        className={cn(
          "size-3.5 shrink-0",
          e.type === "dir" ? "text-sky-600 dark:text-sky-400" : "",
          e.type !== "dir" && "text-muted-foreground"
        )}
      />
      <span
        className={cn(
          "min-w-0 flex-1 truncate",
          changed && "font-medium text-amber-700 dark:text-amber-400"
        )}
      >
        {e.name}
      </span>
      {changed && (
        <span
          className="size-1.5 shrink-0 rounded-full bg-amber-500"
          aria-label="changed"
        />
      )}
      {e.type === "file" && (
        <span className="shrink-0 text-caption text-muted-foreground">
          {formatBytes(e.size)}
        </span>
      )}
    </button>
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
  onBack,
}: {
  env: string
  sandboxId: string
  path: string
  onBack: () => void
}) {
  const content = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes/{id}/files/content",
    { params: { path: { env, id: sandboxId }, query: { path } } }
  )
  const { refetch } = content
  const name = path.split("/").at(-1) ?? path
  const c = content.data

  return (
    <div className="grid min-w-0 content-start gap-2">
      <div className="flex min-w-0 items-center gap-1">
        <Button
          variant="ghost"
          size="icon-xs"
          title="Back to the folder"
          onClick={onBack}
        >
          <ArrowLeftIcon />
        </Button>
        <span
          className="min-w-0 flex-1 truncate font-mono text-caption"
          title={path}
        >
          {path}
        </span>
        {c && (
          <span className="shrink-0 text-caption text-muted-foreground">
            {formatBytes(c.size)}
          </span>
        )}
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
      {c && <FileBody file={c} name={name} path={path} />}
    </div>
  )
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
