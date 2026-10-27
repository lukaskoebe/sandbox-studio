import { CaretRightIcon, GitDiffIcon } from "@phosphor-icons/react"
import { UnifiedDiff } from "@/components/review-feedback"
import { Badge } from "@/components/ui/badge"
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible"
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Spinner } from "@/components/ui/spinner"
import { $api, errorMessage } from "@/lib/api/client"
import type { components } from "@/lib/api/schema.gen"
import { changeCount, type ChatTurn } from "@/lib/chats"
import { cn } from "@/lib/utils"

type FileChange = components["schemas"]["FileChange"]

const statuses: Record<FileChange["status"], { label: string; cls: string }> = {
  added: {
    label: "New",
    cls: "text-emerald-700 dark:text-emerald-400",
  },
  modified: { label: "Changed", cls: "text-sky-700 dark:text-sky-400" },
  deleted: { label: "Deleted", cls: "text-destructive" },
}

/**
 * The files one request changed, with their diffs. The diffs compare the workspace before
 * and after that request, so they stay true after later edits.
 */
export function ChangesPanel({
  env,
  chatId,
  turns,
  turnId,
  onTurnChange,
  label,
}: {
  env: string
  chatId: string
  /** The turns that changed files, newest first. */
  turns: ChatTurn[]
  turnId?: string
  onTurnChange: (id: string) => void
  /** A short name for a turn: its prompt. */
  label: (t: ChatTurn) => string
}) {
  const turn = turns.find((t) => t.id === turnId) ?? turns.at(0)
  if (!turn) {
    return (
      <Empty>
        <EmptyHeader>
          <EmptyMedia variant="icon">
            <GitDiffIcon />
          </EmptyMedia>
          <EmptyTitle>No changes yet</EmptyTitle>
          <EmptyDescription>
            Files your teammate creates, edits or deletes show up here after
            each message.
          </EmptyDescription>
        </EmptyHeader>
      </Empty>
    )
  }
  return (
    <div className="grid min-w-0 content-start gap-3">
      <Select
        value={turn.id}
        onValueChange={(v) => v && onTurnChange(v)}
        items={Object.fromEntries(turns.map((t) => [t.id, label(t)]))}
      >
        <SelectTrigger
          size="sm"
          className="w-full min-w-0"
          aria-label="Which message's changes"
        >
          <SelectValue className="truncate" />
        </SelectTrigger>
        <SelectContent>
          {turns.map((t) => (
            <SelectItem key={t.id} value={t.id}>
              <span className="truncate">{label(t)}</span>
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      <TurnChangesView key={turn.id} env={env} chatId={chatId} turn={turn} />
    </div>
  )
}

function TurnChangesView({
  env,
  chatId,
  turn,
}: {
  env: string
  chatId: string
  turn: ChatTurn
}) {
  const changes = $api.useQuery(
    "get",
    "/api/environments/{env}/chats/{chat}/turns/{turn}/changes",
    { params: { path: { env, chat: chatId, turn: turn.id } } },
    { staleTime: Infinity }
  )
  // The stored list, until the diffs arrive.
  const data = changes.data ?? turn.changes
  const files = data?.files ?? []
  const added = files.reduce((n, f) => n + f.added, 0)
  const removed = files.reduce((n, f) => n + f.removed, 0)
  const count = changeCount(turn)

  return (
    <>
      <p className="flex flex-wrap items-center gap-x-2 gap-y-1 text-muted-foreground">
        {count} {count === 1 ? "file" : "files"} changed
        <span className="text-emerald-600 dark:text-emerald-400">+{added}</span>
        <span className="text-destructive">−{removed}</span>
        {turn.undoneAt && <Badge variant="outline">Undone</Badge>}
        {changes.isPending && <Spinner className="size-3" />}
      </p>
      {turn.undoneAt && (
        <p className="text-muted-foreground">
          You undid these changes; the files are back as they were before.
        </p>
      )}
      {changes.isError && (
        <p className="text-muted-foreground">
          Can't load the details: {errorMessage(changes.error)}
        </p>
      )}
      <ul className="grid min-w-0 gap-1.5">
        {files.map((f) => (
          <li key={f.path} className="min-w-0">
            <FileChangeView file={f} loading={changes.isPending} />
          </li>
        ))}
      </ul>
      {(data?.more ?? 0) > 0 && (
        <p className="text-muted-foreground">
          And {data?.more} more {data?.more === 1 ? "file" : "files"}, too many
          to list here.
        </p>
      )}
    </>
  )
}

function FileChangeView({
  file: f,
  loading,
}: {
  file: FileChange
  loading: boolean
}) {
  const status = statuses[f.status]
  return (
    <Collapsible className="group/file min-w-0 rounded-lg border bg-card">
      <CollapsibleTrigger className="flex w-full min-w-0 items-center gap-2 px-2.5 py-1.5 text-left outline-none focus-visible:ring-2 focus-visible:ring-ring/30">
        <span className={cn("w-14 shrink-0 text-caption", status.cls)}>
          {status.label}
        </span>
        <span
          className="min-w-0 flex-1 truncate font-mono text-caption"
          title={f.path}
          dir="rtl"
        >
          {/* Right-to-left keeps the file name visible when a long path is cut. */}
          <bdi>{f.path}</bdi>
        </span>
        {!f.binary && (f.added > 0 || f.removed > 0) && (
          <span className="shrink-0 text-caption">
            <span className="text-emerald-600 dark:text-emerald-400">
              +{f.added}
            </span>{" "}
            <span className="text-destructive">−{f.removed}</span>
          </span>
        )}
        <CaretRightIcon className="size-3 shrink-0 text-muted-foreground transition-transform group-data-[open]/file:rotate-90" />
      </CollapsibleTrigger>
      <CollapsibleContent className="border-t p-2">
        <FileDiff file={f} loading={loading} />
      </CollapsibleContent>
    </Collapsible>
  )
}

function FileDiff({
  file: f,
  loading,
}: {
  file: FileChange
  loading: boolean
}) {
  const note = (text: string) => <p className="text-muted-foreground">{text}</p>
  if (loading) return note("Loading the changes…")
  if (f.binary)
    return note(
      "This isn't a text file, or it's too large to compare line by line."
    )
  if (f.diff) {
    return (
      <div className="grid gap-1.5">
        <UnifiedDiff diff={f.diff} />
        {f.truncated && note("The rest of this change is too long to show.")}
      </div>
    )
  }
  if (f.truncated) return note("This change is too long to show here.")
  if (f.added === 0 && f.removed === 0) return note("No lines changed.")
  return note("The details of this change are no longer kept.")
}
