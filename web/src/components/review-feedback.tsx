import { lazy, Suspense, type Ref } from "react"
import { toast } from "sonner"
import type { DiffViewHandle } from "@/components/diff-view"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { $api, errorMessage } from "@/lib/api/client"
import { harnessLabels } from "@/lib/teammates"

/** A diff line, with the file and line number a comment on it refers to. */
export type DiffLine = {
  text: string
  /** The file's place in the diff, counting from 0. */
  fileNo: number
  file?: string
  /** The line's number in the new file, or in the old one for a removed line. */
  line?: number
  removed?: boolean
}

/** The last diffs parsed, as a review renders its diff's lines on every keystroke. */
const parsed = new Map<string, DiffLine[]>()

/** Splits a unified diff into lines and numbers the ones a comment can point at. */
export function parseDiff(diff: string): DiffLine[] {
  let lines = parsed.get(diff)
  if (!lines) {
    lines = splitDiff(diff)
    parsed.set(diff, lines)
    if (parsed.size > 4) parsed.delete(parsed.keys().next().value!)
  }
  return lines
}

function splitDiff(diff: string): DiffLine[] {
  let file: string | undefined
  let fileNo = -1
  let oldLine = 0
  let newLine = 0
  // The lines left in the hunk: a removed "-- x" reads "--- x" and isn't a file's header.
  let oldLeft = 0
  let newLeft = 0
  return diff.split("\n").map((text) => {
    if (text.startsWith("diff --git ")) {
      file = text.split(" b/").at(-1)
      fileNo++
      oldLeft = newLeft = 0
      return { text, fileNo }
    }
    // A diff from a chat's snapshot is one file with no diff --git line.
    fileNo = Math.max(fileNo, 0)
    if (oldLeft <= 0 && newLeft <= 0) {
      if (text.startsWith("+++ ") || text.startsWith("--- ")) {
        if (text.startsWith("+++ b/")) file = text.slice(6)
        return { text, fileNo }
      }
      const hunk = /^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@/.exec(text)
      if (hunk) {
        oldLine = Number(hunk[1])
        newLine = Number(hunk[3])
        oldLeft = Number(hunk[2] || 1)
        newLeft = Number(hunk[4] || 1)
        return { text, fileNo }
      }
      return { text, fileNo }
    }
    if (!file) return { text, fileNo }
    if (text.startsWith("+")) {
      newLeft--
      return { text, fileNo, file, line: newLine++ }
    }
    if (text.startsWith("-")) {
      oldLeft--
      return { text, fileNo, file, line: oldLine++, removed: true }
    }
    if (text.startsWith(" ")) {
      oldLeft--
      newLeft--
      oldLine++
      return { text, fileNo, file, line: newLine++ }
    }
    return { text, fileNo }
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

const DiffView = lazy(() => import("@/components/diff-view"))

const noComments: LineComment[] = []

/** A unified diff with comments under its lines; the view and its highlighter load when first shown. */
export function UnifiedDiff({
  diff,
  comments = noComments,
  onComment,
  fill,
  commentLabel,
  ref,
}: {
  diff: string
  comments?: LineComment[]
  onComment?: (comments: LineComment[]) => void
  /** A taller view, with each file's name, for a panel of its own. */
  fill?: boolean
  /** Who wrote the comments, above each one, such as "Ada's draft". */
  commentLabel?: string
  /** For a list of comments that jumps to them. */
  ref?: Ref<DiffViewHandle>
}) {
  if (!diff) return <p className="text-xs text-muted-foreground">No changes.</p>
  return (
    <Suspense
      fallback={
        <p className="text-xs text-muted-foreground">Loading the changes…</p>
      }
    >
      <DiffView
        diff={diff}
        comments={comments}
        onComment={onComment}
        fill={fill}
        commentLabel={commentLabel}
        ref={ref}
      />
    </Suspense>
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
