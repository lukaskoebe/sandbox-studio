import { lazy, Suspense, useRef, type ReactNode, type Ref } from "react"
import { toast } from "sonner"
import type { DiffViewHandle } from "@/components/diff-view"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Kbd } from "@/components/ui/kbd"
import {
  CaretDownIcon,
  CaretUpIcon,
  ChatCircleTextIcon,
} from "@phosphor-icons/react"
import { plural } from "@/lib/approval-kinds"
import { cn } from "@/lib/utils"
import { useComposerSpot } from "@/lib/composer-clearance"
import { $api, errorMessage } from "@/lib/api/client"
import { harnessLabels } from "@/lib/teammates"
import type { DiffPlace } from "@/lib/review-drafts"
import { ErrorBoundary } from "@/components/error-boundary"

/** A diff line, with the file and line number a comment on it refers to. */
export type DiffLine = {
  text: string
  /** The file's place in the diff, counting from 0. */
  fileNo: number
  file?: string
  /** The line's number in the new file, or in the old one for a removed line. */
  line?: number
  /** An unchanged line's number in the old file. */
  oldLine?: number
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
      return { text, fileNo, file, line: newLine++, oldLine: oldLine++ }
    }
    return { text, fileNo }
  })
}

/**
 * A comment on a line of the diff, by the line's index, or on the lines from start to index
 * when it covers several. It shows under its last line.
 */
export type LineComment = { index: number; text: string; start?: number }

/** The most lines of a comment's range the agent gets quoted. */
const quotedLines = 20

/** Where a comment is, such as "cron/billing.go:13" or "cron/billing.go:13–15". */
export function commentWhere(lines: DiffLine[], c: LineComment) {
  const l = lines[c.index]
  const first = c.start !== undefined ? lines[c.start] : undefined
  if (!first || first.line === undefined || c.start === c.index)
    return `${l.file}:${l.line}${l.removed ? " (removed line)" : ""}`
  return `${l.file}:${first.line}–${l.line}`
}

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
          .map((c) => {
            const where = commentWhere(lines, c)
            const text = c.text.trim().replace(/\n/g, "\n  ")
            if (c.start !== undefined && c.start < c.index) {
              const range = lines
                .slice(c.start, c.index + 1)
                .filter((l) => l.line !== undefined)
              const shown = range
                .slice(0, quotedLines)
                .map((l) => `  ${l.text}`)
              if (range.length > quotedLines)
                shown.push(`  … ${range.length - quotedLines} more lines`)
              return `- ${where}, on these lines:\n  \`\`\`diff\n${shown.join("\n")}\n  \`\`\`\n  ${text}`
            }
            const l = lines[c.index]
            const code = l.text.slice(1).trim()
            return `- ${where}${code ? `, on \`${code}\`` : ""}:\n  ${text}`
          })
          .join("\n")
    )
  }
  return parts.join("\n\n")
}

const DiffView = lazy(() => import("@/components/diff-view"))

/** How many of a diff's files the reader marked viewed. */
export type ViewedProgress = { viewed: number; total: number }

/** How many of a review's files are viewed, and the way to the next one that isn't. */
export function ViewedCount({
  progress,
  onNext,
}: {
  progress: ViewedProgress | undefined
  onNext: () => void
}) {
  if (!progress?.total) return null
  const done = progress.viewed === progress.total
  return (
    <span className="flex items-center gap-2">
      <span
        className={cn(
          "font-mono text-caption whitespace-nowrap text-muted-foreground",
          done && "text-emerald-600 dark:text-emerald-400"
        )}
      >
        {progress.viewed} / {progress.total} viewed
      </span>
      {!done && progress.total > 1 && (
        <Button size="xs" variant="outline" onClick={onNext}>
          Next unviewed
        </Button>
      )}
    </span>
  )
}

/** A comment in a review's list: where it is, its text, and the way to it when the diff has it. */
export type CommentEntry = {
  key: string
  where: string
  text: string
  onPick?: () => void
}

/**
 * The review bar's comments, in place of a tab of them: how many, a list that goes to one, and
 * the previous and next one, as p and n do.
 */
export function CommentNav({
  entries,
  onStep,
}: {
  entries: CommentEntry[]
  onStep: (dir: 1 | -1) => void
}) {
  if (!entries.length) return null
  return (
    <span className="flex items-center rounded-md ring-1 ring-foreground/10">
      <DropdownMenu>
        <DropdownMenuTrigger
          render={
            <Button size="xs" variant="ghost" className="rounded-r-none" />
          }
        >
          <ChatCircleTextIcon />
          {plural(entries.length, "comment")}
        </DropdownMenuTrigger>
        <DropdownMenuContent
          aria-label="Comments"
          align="end"
          className="w-80 max-w-[calc(100vw-2rem)]"
        >
          {entries.map((e) => (
            <DropdownMenuItem
              key={e.key}
              disabled={!e.onPick}
              onClick={e.onPick}
              className="grid items-start gap-0.5"
            >
              <span className="font-mono text-caption text-muted-foreground">
                {e.where}
              </span>
              <span className="line-clamp-2 break-words">{e.text}</span>
            </DropdownMenuItem>
          ))}
        </DropdownMenuContent>
      </DropdownMenu>
      <Button
        size="icon-xs"
        variant="ghost"
        aria-label="Previous comment"
        title="Previous comment (p)"
        onClick={() => onStep(-1)}
      >
        <CaretUpIcon />
      </Button>
      <Button
        size="icon-xs"
        variant="ghost"
        aria-label="Next comment"
        title="Next comment (n)"
        onClick={() => onStep(1)}
      >
        <CaretDownIcon />
      </Button>
    </span>
  )
}

/** The keys a review's diff takes, with what each does. */
export const reviewKeys: {
  keys: string[]
  does: string
  comments?: boolean
}[] = [
  { keys: ["j", "k"], does: "Next and previous file" },
  { keys: ["n", "p"], does: "Next and previous comment", comments: true },
  { keys: ["v"], does: "Mark the file viewed and go to the next unviewed one" },
  {
    keys: ["c"],
    does: "Comment on the line in focus: the comment last gone to, or the line at the top",
    comments: true,
  },
  { keys: ["?"], does: "All keys" },
]

/** A line under a review naming its keys, where there is a keyboard to press them. */
export function ReviewKeys({
  comments,
  onHelp,
}: {
  /** Whether c applies: the review takes line comments. */
  comments?: boolean
  onHelp: () => void
}) {
  const short: Record<string, string> = {
    j: "files",
    n: "comments",
    v: "viewed, next",
    c: "comment",
    "?": "all keys",
  }
  return (
    <p className="hidden flex-wrap items-center gap-x-3 gap-y-1 text-caption text-muted-foreground [@media(pointer:fine)]:flex">
      {reviewKeys
        .filter((k) => comments || !k.comments)
        .map((k) => (
          <span key={k.keys[0]} className="flex items-center gap-1">
            {k.keys.map((key) => (
              <Kbd key={key}>{key}</Kbd>
            ))}
            {k.keys[0] === "?" ? (
              <button
                type="button"
                className="underline-offset-2 hover:text-foreground hover:underline"
                onClick={onHelp}
              >
                {short[k.keys[0]]}
              </button>
            ) : (
              short[k.keys[0]]
            )}
          </span>
        ))}
    </p>
  )
}

/**
 * A review's decision row. It sits at the foot of the pane, so the approval stack, floating over
 * a screen that has taken the page, keeps clear of it as it does of a composer.
 */
export function DecisionRow({
  className,
  children,
}: {
  className?: string
  children: ReactNode
}) {
  const ref = useRef<HTMLDivElement>(null)
  useComposerSpot(ref)
  return (
    <div ref={ref} role="group" aria-label="Decision" className={className}>
      {children}
    </div>
  )
}

/** A review without a diff to fill it, or one still loading: it scrolls as a page. */
export function ReviewScroll({ children }: { children: ReactNode }) {
  return (
    <ScrollArea className="min-h-0 flex-1" viewportClassName="p-3 md:p-4">
      <div className="grid min-w-0 gap-3 text-xs">{children}</div>
    </ScrollArea>
  )
}

/** A review whose diff is its one scroll frame: a bar above it and the decision below. */
export const reviewPaneClass =
  "flex min-h-0 min-w-0 flex-1 flex-col gap-2 p-3 text-xs md:p-4"

/**
 * A filling diff's head with a line in place of its files, as while the view loads or when
 * there is nothing it can draw, so the head stays where it would scroll.
 */
export function DiffFrameText({
  header,
  after,
  children,
}: {
  header?: ReactNode
  after?: ReactNode
  children: ReactNode
}) {
  return (
    <ScrollArea className="min-h-0 flex-1 rounded-md ring-1 ring-foreground/10">
      <div className="grid min-w-0 gap-3 p-3 text-xs">
        {header}
        {children}
        {after}
      </div>
    </ScrollArea>
  )
}

const noComments: LineComment[] = []

/** A unified diff with comments under its lines; the view and its highlighter load when first shown. */
export function UnifiedDiff({
  diff,
  comments = noComments,
  onComment,
  fill,
  header,
  after,
  commentLabel,
  ranges,
  layouts,
  place,
  onPlace,
  viewed,
  onViewed,
  keys,
  ref,
}: {
  diff: string
  comments?: LineComment[]
  onComment?: (comments: LineComment[]) => void
  /** Fills its parent's height as a review's one scroll frame, with each file's name. */
  fill?: boolean
  /** What scrolls in above the first file and after the last, when it fills. */
  header?: ReactNode
  after?: ReactNode
  /** Who wrote the comments, above each one, such as "Ada's draft". */
  commentLabel?: string
  /** Whether a comment can cover several lines; on unless the comments go where it can't. */
  ranges?: boolean
  /** Whether it offers stacked or split lines; full-length diffs always do. */
  layouts?: boolean
  /** Where the reader was, to open there, and where they are as they scroll. */
  place?: DiffPlace
  onPlace?: (place: DiffPlace) => void
  /** The branch whose viewed marks it offers (viewedScope), and how many are viewed. */
  viewed?: string
  onViewed?: (progress: ViewedProgress) => void
  /** Whether it takes the review's keys (reviewKeys) while it shows. */
  keys?: boolean
  /** For a list of comments that jumps to them. */
  ref?: Ref<DiffViewHandle>
}) {
  const text = (line: string) =>
    fill ? (
      <DiffFrameText header={header} after={after}>
        <p className="text-muted-foreground">{line}</p>
      </DiffFrameText>
    ) : (
      <p className="text-xs text-muted-foreground">{line}</p>
    )
  if (!diff) return text("No changes.")
  return (
    <ErrorBoundary label="the changes" resetKey={diff} className="flex-none">
      <Suspense fallback={text("Loading the changes…")}>
        <DiffView
          diff={diff}
          comments={comments}
          onComment={onComment}
          fill={fill}
          header={header}
          after={after}
          commentLabel={commentLabel}
          ranges={ranges}
          layouts={layouts}
          place={place}
          onPlace={onPlace}
          viewed={viewed}
          onViewed={onViewed}
          keys={keys}
          ref={ref}
        />
      </Suspense>
    </ErrorBoundary>
  )
}

/** Where review feedback goes: "" for the agent's next fetch or prompt, or a chat or terminal. */
export type FeedbackTarget = string

/** The target that starts a chat in the sandbox with the feedback as its first prompt. */
export const newChatTarget = "new"

/**
 * The sandbox's chats and agent terminals that can take review feedback right away, and a
 * new chat when the sandbox has a teammate to run it. A terminal counts when an agent runs
 * in it, whether Studio started it or someone did by hand; a chat continued in the terminal
 * is reached through that terminal. A plain shell is left out, as text typed into it would
 * run as commands.
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
    if (!t.agent) continue
    options[`terminal:${t.name}`] =
      `Terminal ${t.name} · ${label(t.harness || t.program || "")}`
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

/** How comments left on a waiting push reach its agent. */
export const leftHint =
  "The agent reads them when it checks on the push or gets its next prompt."

/**
 * Sends review feedback to the chosen chat or terminal, or leaves it on the waiting push
 * when none is chosen, and says where it went.
 */
export function useSendFeedback() {
  return $api.useMutation(
    "post",
    "/api/environments/{env}/approvals/{id}/feedback",
    {
      onSuccess: (out) =>
        out.left
          ? toast.success("Comments left on the push", {
              description: leftHint,
            })
          : toast.success(
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

/** The request body for a target; with none, the comments stay on the waiting push. */
export function feedbackBody(target: FeedbackTarget, note: string) {
  if (!target) return { note }
  if (target === newChatTarget) return { note, newChat: true }
  const [kind, id] = [
    target.slice(0, target.indexOf(":")),
    target.slice(target.indexOf(":") + 1),
  ]
  return kind === "chat" ? { note, chat: id } : { note, terminal: id }
}
