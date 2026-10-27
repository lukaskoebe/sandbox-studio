import { useState } from "react"
import {
  ArrowUpIcon,
  FolderIcon,
  GitBranchIcon,
  HouseIcon,
} from "@phosphor-icons/react"
import { Ident } from "@/components/ident"
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
import { FieldError } from "@/components/ui/field"
import { Spinner } from "@/components/ui/spinner"
import { $api, errorMessage } from "@/lib/api/client"

/**
 * Browses the folders of the computer Studio runs on, which the browser can't name, and picks
 * one. It opens at start (a path as a form holds it, such as ~/git), or at the home directory.
 */
export function FolderPickerDialog({
  open,
  onOpenChange,
  start,
  onPick,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  start?: string
  /** The picked folder, as ~/... when it is in the home directory. */
  onPick: (path: string) => void
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        {open && (
          <FolderBrowser
            start={start?.trim() ?? ""}
            onPick={(p) => {
              onPick(p)
              onOpenChange(false)
            }}
            onCancel={() => onOpenChange(false)}
          />
        )}
      </DialogContent>
    </Dialog>
  )
}

function FolderBrowser({
  start,
  onPick,
  onCancel,
}: {
  start: string
  onPick: (path: string) => void
  onCancel: () => void
}) {
  const [path, setPath] = useState(start)
  const [hidden, setHidden] = useState(false)
  const folder = $api.useQuery("get", "/api/system/folders", {
    params: { query: { path, hidden } },
  })
  const f = folder.data
  const entries = f?.entries ?? []
  const repos = entries.filter((e) => e.git).length
  return (
    <div className="grid gap-3">
      <DialogHeader>
        <DialogTitle>Choose a folder</DialogTitle>
        <DialogDescription>
          Folders on this computer. Pick the one your repositories are in.
        </DialogDescription>
      </DialogHeader>
      <div className="flex items-center gap-1">
        <Button
          type="button"
          variant="ghost"
          size="icon-sm"
          title="Up one folder"
          aria-label="Up one folder"
          disabled={!f?.parent}
          onClick={() => f?.parent && setPath(f.parent)}
        >
          <ArrowUpIcon />
        </Button>
        <Button
          type="button"
          variant="ghost"
          size="icon-sm"
          title="Home folder"
          aria-label="Home folder"
          onClick={() => setPath("")}
        >
          <HouseIcon />
        </Button>
        <span className="min-w-0 flex-1 truncate text-xs">
          {f ? <Ident>{f.short}</Ident> : path || "~"}
        </span>
        <label className="flex shrink-0 items-center gap-1.5 text-xs text-muted-foreground">
          <Checkbox
            checked={hidden}
            onCheckedChange={(c) => setHidden(c === true)}
          />
          Hidden folders
        </label>
      </div>
      <div
        className="h-72 overflow-y-auto rounded-md border"
        role="list"
        aria-label="Folders"
      >
        {folder.isPending ? (
          <div className="flex h-full items-center justify-center">
            <Spinner />
          </div>
        ) : folder.error ? (
          <div className="p-3">
            <FieldError>{errorMessage(folder.error)}</FieldError>
          </div>
        ) : f && entries.length === 0 ? (
          <p className="p-3 text-xs text-muted-foreground">No folders here.</p>
        ) : (
          entries.map((e) => (
            <button
              key={e.path}
              type="button"
              role="listitem"
              className="flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs outline-none hover:bg-muted focus-visible:bg-muted"
              onClick={() => setPath(e.path)}
            >
              {e.git ? (
                <GitBranchIcon className="size-4 shrink-0 text-primary" />
              ) : (
                <FolderIcon className="size-4 shrink-0 text-muted-foreground" />
              )}
              <span className="min-w-0 flex-1 truncate">{e.name}</span>
              {e.git && (
                <span className="text-xs text-muted-foreground">
                  repository
                </span>
              )}
            </button>
          ))
        )}
      </div>
      {f && (
        <p className="text-xs text-muted-foreground">
          {f.git
            ? "This folder is a repository itself. A forge is the folder the repositories are in, such as the one above."
            : repos === 1
              ? "1 repository in this folder."
              : `${repos} repositories in this folder.`}
          {f.truncated && " Only the first 1000 folders are listed."}
        </p>
      )}
      <DialogFooter>
        <Button type="button" variant="outline" onClick={onCancel}>
          Cancel
        </Button>
        <Button
          type="button"
          disabled={!f}
          onClick={() => f && onPick(f.short)}
        >
          Use this folder
        </Button>
      </DialogFooter>
    </div>
  )
}
