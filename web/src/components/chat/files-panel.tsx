import { useMemo } from "react"
import {
  ArrowClockwiseIcon,
  ArrowLeftIcon,
  DownloadSimpleIcon,
  FileIcon,
  FolderIcon,
  LinkSimpleIcon,
} from "@phosphor-icons/react"
import { Button } from "@/components/ui/button"
import { ScrollArea, ScrollBar } from "@/components/ui/scroll-area"
import { Spinner } from "@/components/ui/spinner"
import { $api, errorMessage } from "@/lib/api/client"
import type { components } from "@/lib/api/schema.gen"
import { cn, formatBytes } from "@/lib/utils"

type FileEntry = components["schemas"]["FileEntry"]
type FileContent = components["schemas"]["FileContent"]

function join(dir: string, name: string) {
  return dir ? `${dir}/${name}` : name
}

/**
 * A read-only browser of the sandbox's /workspace: one folder at a time, and a viewer for
 * text files and images. Files the selected request changed are marked.
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
  const entries = list.data?.entries ?? []

  return (
    <div className="grid min-w-0 content-start gap-2">
      <div className="flex min-w-0 items-center gap-1">
        <nav
          aria-label="Folder"
          className="flex min-w-0 flex-1 flex-wrap items-center gap-0.5 font-mono text-[0.6875rem]"
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
      </div>
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
        <span className="shrink-0 text-[0.6875rem] text-muted-foreground">
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
          className="min-w-0 flex-1 truncate font-mono text-[0.6875rem]"
          title={path}
        >
          {path}
        </span>
        {c && (
          <span className="shrink-0 text-[0.6875rem] text-muted-foreground">
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
      {c && <FileBody file={c} name={name} />}
    </div>
  )
}

function FileBody({ file: c, name }: { file: FileContent; name: string }) {
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
  return (
    <ScrollArea
      className="rounded-md bg-muted/40 ring-1 ring-foreground/10"
      viewportClassName="max-h-[70vh]"
    >
      <pre className="p-2 font-mono text-[11px]/[1.45] whitespace-pre">
        {text}
      </pre>
      <ScrollBar orientation="horizontal" />
    </ScrollArea>
  )
}
