import { useState } from "react"
import { toast } from "sonner"
import { ChatCircleTextIcon, XIcon } from "@phosphor-icons/react"
import { Button } from "@/components/ui/button"
import { ScrollArea, ScrollBar } from "@/components/ui/scroll-area"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { textareaClass } from "@/components/memory-dialogs"
import { $api, errorMessage } from "@/lib/api/client"
import { harnessLabels } from "@/lib/teammates"
import { cn } from "@/lib/utils"

/** A diff line, with the file and line number a comment on it refers to. */
export type DiffLine = {
  text: string
  file?: string
  /** The line's number in the new file, or in the old one for a removed line. */
  line?: number
  removed?: boolean
}

/** Splits a unified diff into lines and numbers the ones a comment can point at. */
export function parseDiff(diff: string): DiffLine[] {
  let file: string | undefined
  let oldLine = 0
  let newLine = 0
  return diff.split("\n").map((text) => {
    if (text.startsWith("diff --git ")) {
      file = text.split(" b/").at(-1)
      return { text }
    }
    if (text.startsWith("+++ ") || text.startsWith("--- ")) {
      if (text.startsWith("+++ b/")) file = text.slice(6)
      return { text }
    }
    const hunk = /^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/.exec(text)
    if (hunk) {
      oldLine = Number(hunk[1])
      newLine = Number(hunk[2])
      return { text }
    }
    if (!file || !newLine) return { text }
    if (text.startsWith("+")) return { text, file, line: newLine++ }
    if (text.startsWith("-"))
      return { text, file, line: oldLine++, removed: true }
    if (text.startsWith(" ")) {
      oldLine++
      return { text, file, line: newLine++ }
    }
    return { text }
  })
}

/** A comment on one line of the diff, by the line's index. */
export type LineComment = { index: number; text: string }

/** The note the agent gets: the general note, then each line comment with where it is. */
export function composeNote(
  note: string,
  lines: DiffLine[],
  comments: LineComment[]
) {
  const parts = note.trim() ? [note.trim()] : []
  const sorted = [...comments].sort((a, b) => a.index - b.index)
  if (sorted.length) {
    parts.push(
      "Comments on the diff:\n" +
        sorted
          .map(({ index, text }) => {
            const l = lines[index]
            const where = `${l.file}:${l.line}${l.removed ? " (removed line)" : ""}`
            const code = l.text.slice(1).trim()
            return `- ${where}${code ? `, on \`${code}\`` : ""}:\n  ${text.trim().replace(/\n/g, "\n  ")}`
          })
          .join("\n")
    )
  }
  return parts.join("\n\n")
}

function lineClass(line: string) {
  if (line.startsWith("+++") || line.startsWith("---"))
    return "text-muted-foreground"
  if (line.startsWith("+"))
    return "bg-emerald-500/10 text-emerald-700 dark:text-emerald-300"
  if (line.startsWith("-")) return "bg-destructive/10 text-destructive"
  if (line.startsWith("@@")) return "text-sky-600 dark:text-sky-400"
  if (line.startsWith("diff --git"))
    return "mt-2 font-semibold text-foreground first:mt-0"
  if (line.startsWith("[")) return "text-amber-600 dark:text-amber-400"
  return ""
}

/**
 * A unified diff, colored line by line. With onComment, a line's button adds a comment under
 * it, and the comments show where they were made.
 */
export function UnifiedDiff({
  diff,
  comments = [],
  onComment,
}: {
  diff: string
  comments?: LineComment[]
  onComment?: (comments: LineComment[]) => void
}) {
  const [editing, setEditing] = useState<number>()
  const [draft, setDraft] = useState("")
  if (!diff) return <p className="text-xs text-muted-foreground">No changes.</p>
  const lines = parseDiff(diff)
  const save = () => {
    if (editing === undefined || !onComment) return
    const rest = comments.filter((c) => c.index !== editing)
    onComment(
      draft.trim() ? [...rest, { index: editing, text: draft.trim() }] : rest
    )
    setEditing(undefined)
  }
  return (
    <ScrollArea
      className="rounded-md bg-muted/40 ring-1 ring-foreground/10"
      viewportClassName="max-h-[60vh]"
    >
      <pre className="py-2 font-mono text-[11px]/[1.45]">
        {lines.map((l, i) => {
          const comment = comments.find((c) => c.index === i)
          const where = l.file && `${l.file}:${l.line}`
          return (
            <div key={i}>
              <div
                className={cn(
                  "group relative px-2 whitespace-pre",
                  onComment && "pl-6",
                  lineClass(l.text)
                )}
              >
                {onComment && l.line !== undefined && (
                  <button
                    type="button"
                    aria-label={`Comment on ${where}`}
                    title={`Comment on ${where}`}
                    className="absolute top-0 left-1 grid size-4 place-items-center rounded-sm text-muted-foreground opacity-0 group-hover:opacity-100 hover:bg-foreground/10 hover:text-foreground focus-visible:opacity-100"
                    onClick={() => {
                      setEditing(i)
                      setDraft(comment?.text ?? "")
                    }}
                  >
                    <ChatCircleTextIcon className="size-3" />
                  </button>
                )}
                {l.text || " "}
              </div>
              {editing === i ? (
                <div className="mx-2 my-1 grid gap-1.5 rounded-md bg-background p-2 font-sans whitespace-normal ring-1 ring-foreground/10">
                  <textarea
                    aria-label={`Comment on ${where}`}
                    autoFocus
                    rows={2}
                    maxLength={2000}
                    value={draft}
                    onChange={(e) => setDraft(e.target.value)}
                    onKeyDown={(e) => {
                      if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) save()
                      if (e.key === "Escape") setEditing(undefined)
                    }}
                    className={textareaClass}
                  />
                  <div className="flex justify-end gap-1.5">
                    <Button
                      size="xs"
                      variant="ghost"
                      onClick={() => setEditing(undefined)}
                    >
                      Cancel
                    </Button>
                    <Button size="xs" onClick={save}>
                      Save comment
                    </Button>
                  </div>
                </div>
              ) : (
                comment && (
                  <div
                    role="note"
                    aria-label={`Comment on ${where}`}
                    className="mx-2 my-1 flex items-start gap-2 rounded-md bg-background px-2 py-1.5 font-sans text-xs whitespace-pre-wrap ring-1 ring-foreground/10"
                  >
                    <ChatCircleTextIcon className="mt-0.5 shrink-0 text-muted-foreground" />
                    <button
                      type="button"
                      className="min-w-0 flex-1 text-left"
                      title="Edit the comment"
                      onClick={() => {
                        setEditing(i)
                        setDraft(comment.text)
                      }}
                    >
                      {comment.text}
                    </button>
                    {onComment && (
                      <Button
                        size="icon-xs"
                        variant="ghost"
                        aria-label="Remove the comment"
                        onClick={() =>
                          onComment(comments.filter((c) => c.index !== i))
                        }
                      >
                        <XIcon />
                      </Button>
                    )}
                  </div>
                )
              )}
            </div>
          )
        })}
      </pre>
      <ScrollBar orientation="horizontal" />
    </ScrollArea>
  )
}

/** Where review feedback goes: "" for the agent's next fetch or prompt, or a chat or terminal. */
export type FeedbackTarget = string

/** The target that starts a chat in the sandbox with the feedback as its first prompt. */
export const newChatTarget = "new"

/**
 * The sandbox's chats and agent terminals that can take review feedback right away, and a
 * new chat when the sandbox has a teammate to run it. A chat continued in the terminal is
 * reached through that terminal; a plain shell is left out, as text typed into it would run
 * as commands.
 */
export function FeedbackTargetSelect({
  env,
  sandboxId,
  newChat,
  value,
  onChange,
}: {
  env: string
  sandboxId: string
  newChat?: boolean
  value: FeedbackTarget
  onChange: (value: FeedbackTarget) => void
}) {
  const path = { params: { path: { env, id: sandboxId } } }
  const chats = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes/{id}/chats",
    path
  )
  const terminals = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes/{id}/terminals",
    path,
    { retry: false }
  )
  const labels: Record<string, string | undefined> = harnessLabels
  const label = (harness: string) => labels[harness] ?? harness
  const options: Record<string, string> = {
    "": "Its next fetch or prompt",
  }
  if (newChat) options[newChatTarget] = "A new chat"
  for (const c of chats.data ?? []) {
    if (c.state !== "idle" && c.state !== "running") continue
    options[`chat:${c.id}`] =
      `Chat “${c.title || "Untitled"}” · ${label(c.harness)}` +
      (c.state === "running" ? " (after its turn)" : "")
  }
  for (const t of terminals.data ?? []) {
    if (!t.harness) continue
    options[`terminal:${t.name}`] = `Terminal ${t.name} · ${label(t.harness)}`
  }
  if (Object.keys(options).length === 1) return null
  return (
    <div className="flex min-w-0 items-center gap-2 text-xs">
      <span id="feedback-target" className="shrink-0 text-muted-foreground">
        Send to
      </span>
      <Select
        value={value}
        items={options}
        onValueChange={(v) => onChange(v ?? "")}
      >
        <SelectTrigger
          size="sm"
          aria-labelledby="feedback-target"
          className="min-w-0 flex-1"
        >
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {Object.entries(options).map(([v, text]) => (
            <SelectItem key={v} value={v}>
              {text}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </div>
  )
}

/** Sends review feedback to the chosen chat or terminal, and says where it went. */
export function useSendFeedback() {
  return $api.useMutation(
    "post",
    "/api/environments/{env}/approvals/{id}/feedback",
    {
      onSuccess: (out) =>
        toast.success(
          out.queued
            ? "Feedback queued; the agent gets it when its turn ends"
            : out.chat
              ? "Feedback sent to a new chat"
              : "Feedback sent to the agent"
        ),
      onError: (err) =>
        toast.error("Could not send the feedback", {
          description: errorMessage(err),
        }),
    }
  )
}

/** The request body for a target. */
export function feedbackBody(target: FeedbackTarget, note: string) {
  if (target === newChatTarget) return { note, newChat: true }
  const [kind, id] = [
    target.slice(0, target.indexOf(":")),
    target.slice(target.indexOf(":") + 1),
  ]
  return kind === "chat" ? { note, chat: id } : { note, terminal: id }
}
