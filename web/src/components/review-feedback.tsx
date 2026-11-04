import {
  memo,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react"
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
import { Tokens, useDiffTokens, type LineTokens } from "@/components/code-view"
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

function lineClass(line: string, highlighted: boolean) {
  if (line.startsWith("+++") || line.startsWith("---"))
    return "text-muted-foreground"
  // A highlighted line keeps its syntax's colors on the side's background.
  if (highlighted && line.startsWith("+")) return "bg-emerald-500/10"
  if (highlighted && line.startsWith("-")) return "bg-destructive/10"
  if (line.startsWith("+"))
    return "bg-emerald-500/10 text-emerald-700 dark:text-emerald-300"
  if (line.startsWith("-")) return "bg-destructive/10 text-destructive"
  if (line.startsWith("@@")) return "text-sky-600 dark:text-sky-400"
  if (line.startsWith("diff --git"))
    return "mt-2 font-semibold text-foreground first:mt-0"
  if (line.startsWith("[")) return "text-amber-600 dark:text-amber-400"
  return ""
}

/** Lines per chunk: a chunk renders as a whole, and in a long diff only near the view. */
const chunkLines = 120
/** A diff up to this many lines renders at once. */
const eagerLines = 600
/** How far beyond the view a long diff keeps its chunks rendered. */
const nearMargin = "1500px 0px"

/** Calls back whether an element is near the view, until the returned function stops it. */
type Watch = (el: Element, near: (near: boolean) => void) => () => void

/** The closest ancestor that scrolls vertically, or none for the page. */
function scrollParent(el: Element) {
  for (let p = el.parentElement; p; p = p.parentElement) {
    const { overflowY } = getComputedStyle(p)
    if (
      /auto|scroll|overlay/.test(overflowY) &&
      p.scrollHeight > p.clientHeight + 1
    )
      return p
  }
  return null
}

function watchNear(el: Element): Watch & { stop: () => void } {
  const calls = new Map<Element, (near: boolean) => void>()
  const io = new IntersectionObserver(
    (entries) => {
      for (const e of entries) calls.get(e.target)?.(e.isIntersecting)
    },
    { root: scrollParent(el), rootMargin: nearMargin }
  )
  const watch = (target: Element, near: (near: boolean) => void) => {
    calls.set(target, near)
    io.observe(target)
    return () => {
      io.unobserve(target)
      calls.delete(target)
    }
  }
  return Object.assign(watch, { stop: () => io.disconnect() })
}

type Actions = {
  edit: (i: number) => void
  cancel: () => void
  save: (i: number, text: string) => void
  remove: (i: number) => void
}

/**
 * A unified diff, colored line by line and highlighted by each file's language. With onComment, a line's button adds a comment under
 * it, and the comments show where they were made. A long diff renders in chunks, each only while it is near the view, so thousands of
 * lines scroll and take typing elsewhere on the page without lag.
 */
export function UnifiedDiff({
  diff,
  comments = noComments,
  onComment,
  fill,
  lineId,
  commentLabel,
}: {
  diff: string
  comments?: LineComment[]
  onComment?: (comments: LineComment[]) => void
  /** Grows with the diff instead of scrolling it at 60vh, for a panel of its own. */
  fill?: boolean
  /** The element ID of line i, for a list of comments that jumps to them. */
  lineId?: (i: number) => string
  /** Who wrote the comments, above each one, such as "Ada's draft". */
  commentLabel?: string
}) {
  const [editing, setEditing] = useState<number>()
  const lines = useMemo(() => parseDiff(diff), [diff])
  const tokens = useDiffTokens(lines)
  const latest = useRef({ comments, onComment })
  useLayoutEffect(() => {
    latest.current = { comments, onComment }
  })
  const actions = useMemo<Actions>(() => {
    const set = (i: number, text?: string) => {
      const { comments: all, onComment: change } = latest.current
      const rest = all.filter((c) => c.index !== i)
      change?.(text ? [...rest, { index: i, text }] : rest)
    }
    return {
      edit: setEditing,
      cancel: () => setEditing(undefined),
      save: (i, text) => {
        set(i, text.trim())
        setEditing(undefined)
      },
      remove: (i) => set(i),
    }
  }, [])
  const byChunk = useMemo(() => {
    const out = new Map<number, LineComment[]>()
    for (const c of comments) {
      const k = Math.floor(c.index / chunkLines)
      out.set(k, [...(out.get(k) ?? []), c])
    }
    return out
  }, [comments])
  const [pre, setPre] = useState<HTMLPreElement | null>(null)
  const lazy = lines.length > eagerLines
  const watch = useMemo(
    () => (pre && lazy ? watchNear(pre) : undefined),
    [pre, lazy]
  )
  useEffect(() => () => watch?.stop(), [watch])
  if (!diff) return <p className="text-xs text-muted-foreground">No changes.</p>
  const chunks = []
  for (let start = 0; start < lines.length; start += chunkLines) {
    const k = start / chunkLines
    chunks.push(
      <DiffChunk
        key={k}
        lines={lines}
        start={start}
        end={Math.min(start + chunkLines, lines.length)}
        tokens={tokens}
        comments={byChunk.get(k)}
        editing={
          editing !== undefined && Math.floor(editing / chunkLines) === k
            ? editing
            : undefined
        }
        actions={actions}
        commentable={!!onComment}
        commentLabel={commentLabel}
        lineId={lineId}
        // The first chunks render before the view is known, so a diff never opens blank.
        lazy={lazy && k > 1}
        watch={watch}
      />
    )
  }
  return (
    <ScrollArea
      className="rounded-md bg-muted/40 ring-1 ring-foreground/10"
      viewportClassName={fill ? undefined : "max-h-[60vh]"}
    >
      <pre ref={setPre} className="py-2 font-mono text-caption/[1.45]">
        {chunks}
      </pre>
      <ScrollBar orientation="horizontal" />
    </ScrollArea>
  )
}

const noComments: LineComment[] = []

type ChunkProps = {
  lines: DiffLine[]
  start: number
  end: number
  tokens: LineTokens
  comments?: LineComment[]
  editing?: number
  actions: Actions
  commentable: boolean
  commentLabel?: string
  lineId?: (i: number) => string
  lazy: boolean
  watch?: Watch
}

/**
 * Lines start to end of a diff. While it is far from the view it is a stand-in exactly as tall, as each line is one line high, and as
 * wide as its longest line; a chunk with a comment always renders, so a jump to the comment finds it.
 */
const DiffChunk = memo(
  function Chunk({
    lines,
    start,
    end,
    tokens,
    comments,
    editing,
    actions,
    commentable,
    commentLabel,
    lineId,
    lazy,
    watch,
  }: ChunkProps) {
    const [near, setNear] = useState(!lazy)
    const ref = useRef<HTMLDivElement>(null)
    useEffect(() => {
      if (!lazy || !watch || !ref.current) return
      return watch(ref.current, setNear)
    }, [lazy, watch])
    const shown = !lazy || near || !!comments?.length || editing !== undefined
    const width = useMemo(() => {
      let max = 0
      for (let i = start; i < end; i++)
        max = Math.max(max, lines[i].text.replace(/\t/g, "        ").length)
      return max
    }, [lines, start, end])
    if (!shown)
      return (
        <div
          ref={ref}
          aria-hidden
          style={{
            // The pre's line height: text-caption/[1.45].
            height: `calc(${end - start} * var(--text-caption) * 1.45)`,
            width: `calc(${width}ch + ${commentable ? 2 : 1}rem)`,
          }}
        />
      )
    const rows = []
    for (let i = start; i < end; i++) {
      const l = lines[i]
      const comment = comments?.find((c) => c.index === i)
      const where = l.file && `${l.file}:${l.line}`
      const code = tokens.get(i)
      rows.push(
        <div key={i} id={lineId?.(i)} className="scroll-mt-12">
          <div
            className={cn(
              "group relative px-2 whitespace-pre",
              commentable && "pl-6",
              lineClass(l.text, code !== undefined)
            )}
          >
            {commentable && l.line !== undefined && (
              <button
                type="button"
                aria-label={`Comment on ${where}`}
                title={`Comment on ${where}`}
                className="absolute top-0 left-1 grid size-4 place-items-center rounded-sm text-muted-foreground opacity-0 group-hover:opacity-100 hover:bg-foreground/10 hover:text-foreground focus-visible:opacity-100 [@media(hover:none)]:opacity-100"
                onClick={() => actions.edit(i)}
              >
                <ChatCircleTextIcon className="size-3" />
              </button>
            )}
            {code ? (
              <>
                <span
                  className={cn(
                    l.text[0] === "+" &&
                      "text-emerald-700 dark:text-emerald-300",
                    l.text[0] === "-" && "text-destructive"
                  )}
                >
                  {l.text[0]}
                </span>
                <Tokens tokens={code} />
              </>
            ) : (
              l.text || " "
            )}
          </div>
          {editing === i ? (
            <CommentEditor
              where={where ?? ""}
              initial={comment?.text ?? ""}
              onSave={(text) => actions.save(i, text)}
              onCancel={actions.cancel}
            />
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
                  onClick={() => actions.edit(i)}
                >
                  {commentLabel && (
                    <span className="block text-caption text-muted-foreground">
                      {commentLabel} · line {l.line}
                    </span>
                  )}
                  {comment.text}
                </button>
                {commentable && (
                  <Button
                    size="icon-xs"
                    variant="ghost"
                    aria-label="Remove the comment"
                    onClick={() => actions.remove(i)}
                  >
                    <XIcon />
                  </Button>
                )}
              </div>
            )
          )}
        </div>
      )
    }
    return <div ref={ref}>{rows}</div>
  },
  // A new lineId function each render names the same IDs, so it doesn't render the chunk again.
  (a, b) =>
    (Object.keys(a) as (keyof ChunkProps)[]).every(
      (k) => k === "lineId" || a[k] === b[k]
    )
)

/** The editor of one line's comment, holding its draft so typing renders only itself. */
function CommentEditor({
  where,
  initial,
  onSave,
  onCancel,
}: {
  where: string
  initial: string
  onSave: (text: string) => void
  onCancel: () => void
}) {
  const [draft, setDraft] = useState(initial)
  return (
    <div className="mx-2 my-1 grid gap-1.5 rounded-md bg-background p-2 font-sans whitespace-normal ring-1 ring-foreground/10">
      <textarea
        aria-label={`Comment on ${where}`}
        autoFocus
        rows={2}
        maxLength={2000}
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) onSave(draft)
          if (e.key === "Escape") onCancel()
        }}
        className={textareaClass}
      />
      <div className="flex justify-end gap-1.5">
        <Button size="xs" variant="ghost" onClick={onCancel}>
          Cancel
        </Button>
        <Button size="xs" onClick={() => onSave(draft)}>
          Save comment
        </Button>
      </div>
    </div>
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
