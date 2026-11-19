import { useRef } from "react"
import {
  CheckIcon,
  CircleNotchIcon,
  FileArrowUpIcon,
  WarningIcon,
  XIcon,
} from "@phosphor-icons/react"
import { Button } from "@/components/ui/button"
import { cn } from "@/lib/utils"

/** A page's request for files: Studio's studio_file_chooser message. */
export type FileRequest = {
  /** Names the request in the answer; missing when Studio can't answer it. */
  id?: string
  multiple: boolean
}

/** What became of files given to the page: sending, given, or why not. */
export type FilesState =
  | { state: "sending"; text: string }
  | { state: "given"; text: string }
  | { state: "failed"; text: string }

/** The label for files on their way or given: one by its name, more by their count. */
export function filesLabel(files: File[]) {
  return files.length === 1 ? files[0].name : `${files.length} files`
}

/**
 * The card over the live view when the page asks for files: headless Chromium has no dialog of
 * its own, so the user picks them from this computer. It also shows files on their way to the
 * page, and what became of them.
 */
export function FileRequestCard({
  name,
  request,
  files,
  onChoose,
  onCancel,
  onDismiss,
}: {
  name: string
  request?: FileRequest
  files?: FilesState
  onChoose: (files: File[]) => void
  onCancel: () => void
  onDismiss: () => void
}) {
  const input = useRef<HTMLInputElement>(null)
  if (!request && !files) return null
  const card =
    "absolute inset-x-0 bottom-3 z-10 mx-auto flex w-fit max-w-[calc(100%-1.5rem)] items-center gap-3 rounded-(--radius) border bg-popover px-3 py-2.5 text-sm text-popover-foreground shadow-lg"
  if (files)
    return (
      <div
        role={files.state === "failed" ? "alert" : "status"}
        className={card}
      >
        {files.state === "sending" ? (
          <CircleNotchIcon className="size-4 shrink-0 animate-spin text-muted-foreground" />
        ) : files.state === "given" ? (
          <CheckIcon className="size-4 shrink-0 text-primary" />
        ) : (
          <WarningIcon className="size-4 shrink-0 text-destructive" />
        )}
        <span className="min-w-0 truncate">{files.text}</span>
        {files.state === "failed" && (
          <Button
            size="icon-sm"
            variant="ghost"
            aria-label="Dismiss"
            onClick={onDismiss}
          >
            <XIcon />
          </Button>
        )}
      </div>
    )
  if (!request) return null
  if (!request.id)
    return (
      <div role="alert" className={card}>
        <FileArrowUpIcon className="size-4 shrink-0 text-muted-foreground" />
        <span className="min-w-0">
          The page asked for files in a way Studio can&apos;t answer. Drop them
          on the page instead.
        </span>
        <Button size="sm" variant="ghost" onClick={onCancel}>
          Dismiss
        </Button>
      </div>
    )
  return (
    <div
      role="dialog"
      aria-label="The page asks for files"
      className={cn(card, "items-start")}
    >
      <FileArrowUpIcon className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
      <span className="grid min-w-0 gap-2">
        <span className="grid gap-0.5">
          <span className="font-medium">
            The page asks for {request.multiple ? "files" : "a file"}
          </span>
          <span className="text-xs text-muted-foreground">
            From this computer, to {name}&apos;s browser. {name} sees what the
            page shows of {request.multiple ? "them" : "it"}.
          </span>
        </span>
        <span className="flex gap-1.5">
          <Button size="sm" autoFocus onClick={() => input.current?.click()}>
            Choose {request.multiple ? "files" : "a file"}…
          </Button>
          <Button size="sm" variant="ghost" onClick={onCancel}>
            Cancel
          </Button>
        </span>
      </span>
      <input
        ref={input}
        type="file"
        multiple={request.multiple}
        className="hidden"
        aria-hidden
        tabIndex={-1}
        onChange={(e) => {
          const chosen = [...(e.currentTarget.files ?? [])]
          e.currentTarget.value = ""
          if (chosen.length) onChoose(chosen)
        }}
      />
    </div>
  )
}

/** Shown over the frame while files are dragged over it. */
export function DropOverlay({
  name,
  driving,
}: {
  name: string
  driving: boolean
}) {
  return (
    <div
      aria-hidden
      className="pointer-events-none absolute inset-0 z-10 flex items-center justify-center bg-background/70 p-4 text-center text-sm font-medium ring-2 ring-primary ring-inset"
    >
      {driving
        ? `Drop to give the page these files, in ${name}'s browser`
        : "Take the wheel to drop files on the page"}
    </div>
  )
}
