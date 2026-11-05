import { useImperativeHandle, useMemo, useRef, useState, type Ref } from "react"
import {
  parsePatchFiles,
  registerCustomTheme,
  type CodeViewDiffItem,
  type DiffLineAnnotation,
} from "@pierre/diffs"
import {
  CodeView,
  WorkerPoolContext,
  type CodeViewHandle,
  type CodeViewReactOptions,
} from "@pierre/diffs/react"
import { getOrCreateWorkerPoolSingleton } from "@pierre/diffs/worker"
import DiffsWorker from "@pierre/diffs/worker/worker-portable.js?worker"
import { ChatCircleTextIcon, XIcon } from "@phosphor-icons/react"
import { Button } from "@/components/ui/button"
import { textareaClass } from "@/components/memory-dialogs"
import {
  parseDiff,
  type DiffLine,
  type LineComment,
} from "@/components/review-feedback"
import { cn } from "@/lib/utils"

/**
 * Token colors as Studio's own variables, so they follow the style and the scheme without
 * highlighting again. Shiki only takes a hex color in a theme, so each stands for a variable.
 */
const palette: [string, string, string[]][] = [
  ["#000002", "var(--muted-foreground)", ["comment", "string.comment"]],
  [
    "#000003",
    "var(--code-keyword)",
    [
      "keyword",
      "storage",
      "keyword.control",
      "storage.type",
      "storage.modifier",
    ],
  ],
  ["#000004", "var(--code-string)", ["string", "markup.inline.raw"]],
  [
    "#000005",
    "var(--code-operator)",
    [
      "constant.numeric",
      "constant.language",
      "constant.character",
      "keyword.operator",
    ],
  ],
  [
    "#000006",
    "var(--code-meta)",
    [
      "entity.name.type",
      "entity.name.class",
      "entity.other.inherited-class",
      "support.type",
      "support.class",
    ],
  ],
  [
    "#000007",
    "var(--code-variable)",
    ["entity.name.function", "support.function"],
  ],
]

registerCustomTheme("studio", async () => ({
  name: "studio",
  type: "dark",
  colors: { "editor.background": "#000000", "editor.foreground": "#000001" },
  fg: "#000001",
  bg: "#000000",
  colorReplacements: {
    "#000000": "var(--background)",
    "#000001": "var(--foreground)",
    ...Object.fromEntries(palette.map(([hex, v]) => [hex, v])),
  },
  tokenColors: [
    { settings: { foreground: "#000001", background: "#000000" } },
    ...palette.map(([hex, , scope]) => ({
      scope,
      settings: { foreground: hex },
    })),
  ],
}))

/** Longer lines stay plain: a regex on one could run for a long time. */
const maxTokenLine = 2000

/** The workers stay once started, so the next diff opens without starting them again. */
const pool = () =>
  getOrCreateWorkerPoolSingleton({
    poolOptions: { workerFactory: () => new DiffsWorker(), poolSize: 2 },
    highlighterOptions: {
      theme: "studio",
      lineDiffType: "word",
      tokenizeMaxLineLength: maxTokenLine,
      maxLineDiffLength: maxTokenLine,
    },
  })

/** What a line of a file holds below it. */
type Note =
  { comment: number; text?: undefined } | { comment?: undefined; text: string }

const lineKey = (fileNo: number | string, side: string, line: number) =>
  `${fileNo}:${side}:${line}`
const sideOf = (l: DiffLine) => (l.removed ? "deletions" : "additions")

/** FNV-1a: which diff this is, not a secret. */
function hash(text: string) {
  let h = 0x811c9dc5
  for (let i = 0; i < text.length; i++) {
    h ^= text.charCodeAt(i)
    h = Math.imul(h, 0x01000193)
  }
  return (h >>> 0).toString(36)
}

/**
 * The patch the library reads and what it leaves out: notes Studio writes in a diff, such as
 * a file too large to show. A diff from a chat's snapshot starts at its --- line, which the
 * library reads as a rename.
 */
function readPatch(lines: DiffLine[]) {
  const notes = new Map<number, string[]>()
  const footer: string[] = []
  const hunks = new Set<number>()
  for (const l of lines) {
    if (l.text.startsWith("@@")) hunks.add(l.fileNo)
    if (!l.text.startsWith("[")) continue
    if (hunks.has(l.fileNo)) footer.push(l.text)
    else notes.set(l.fileNo, [...(notes.get(l.fileNo) ?? []), l.text])
  }
  const kept = lines
    .filter((l) => !l.text.startsWith("["))
    .map((l) => l.text)
    .join("\n")
  const header = /^--- a\/(.+)\n\+\+\+ b\/\1\n/.exec(kept)
  const patch = header
    ? `diff --git a/${header[1]} b/${header[1]}\n${kept}`
    : kept
  const files = parsePatchFiles(patch, {
    // The same diff opened again takes its highlighting from the workers' cache.
    cacheKeyPrefix: `${patch.length}-${hash(patch)}`,
  }).flatMap((p) => p.files)
  return { files, notes, footer }
}

export type DiffViewHandle = {
  /** Brings line i of the diff into view. */
  scrollToLine: (i: number) => void
}

export type DiffViewProps = {
  diff: string
  comments: LineComment[]
  onComment?: (comments: LineComment[]) => void
  fill?: boolean
  commentLabel?: string
  ref?: Ref<DiffViewHandle>
}

/** A unified diff, windowed and highlighted in workers, with comments under its lines. */
export default function DiffView({
  diff,
  comments,
  onComment,
  fill,
  commentLabel,
  ref,
}: DiffViewProps) {
  const lines = parseDiff(diff)
  const [editing, setEditing] = useState<number>()
  const view = useRef<CodeViewHandle<Note, undefined>>(null)
  // A line far from the view unmounts, and the editor on it: the draft outlives it.
  const draft = useRef<{ index: number; text: string; focused: boolean }>(null)
  const { files, notes, footer } = useMemo(() => readPatch(lines), [lines])
  const indexes = useMemo(() => {
    const out = new Map<string, number>()
    lines.forEach((l, i) => {
      if (l.line !== undefined) out.set(lineKey(l.fileNo, sideOf(l), l.line), i)
    })
    return out
  }, [lines])

  // An item keeps its version until its annotations move, so the others don't render again.
  const known = useRef(new Map<number, { key: string; version: number }>())
  const items = useMemo(() => {
    const byFile = new Map<number, DiffLineAnnotation<Note>[]>()
    const add = (fileNo: number, a: DiffLineAnnotation<Note>) =>
      byFile.set(fileNo, [...(byFile.get(fileNo) ?? []), a])
    for (const [fileNo, texts] of notes)
      for (const text of texts)
        add(fileNo, {
          side: "additions",
          lineNumber: 0,
          metadata: { text },
        })
    const marked = new Set(comments.map((c) => c.index))
    if (editing !== undefined) marked.add(editing)
    for (const i of marked) {
      const l = lines[i]
      if (l.line === undefined) continue
      add(l.fileNo, {
        side: sideOf(l),
        lineNumber: l.line,
        metadata: { comment: i },
      })
    }
    return files.map((fileDiff, fileNo): CodeViewDiffItem<Note> => {
      const annotations = byFile.get(fileNo)
      const key =
        diff.length +
        ":" +
        (annotations ?? [])
          .map((a) => `${a.side}${a.lineNumber}`)
          .sort()
          .join()
      const was = known.current.get(fileNo)
      const version = !was ? 0 : was.key === key ? was.version : was.version + 1
      known.current.set(fileNo, { key, version })
      return {
        id: String(fileNo),
        type: "diff",
        fileDiff,
        annotations,
        version,
      }
    })
  }, [diff, files, notes, comments, editing, lines])

  useImperativeHandle(
    ref,
    () => ({
      scrollToLine: (i) => {
        const l = lines[i]
        if (l.line === undefined) return
        view.current?.scrollTo({
          type: "line",
          id: String(l.fileNo),
          lineNumber: l.line,
          side: sideOf(l),
          align: "center",
        })
      },
    }),
    [lines]
  )

  const commentable = !!onComment
  const options = useMemo<CodeViewReactOptions<Note, undefined>>(
    () => ({
      theme: "studio",
      diffStyle: "unified",
      overflow: "scroll",
      lineDiffType: "word",
      tokenizeMaxLineLength: maxTokenLine,
      maxLineDiffLength: maxTokenLine,
      stickyHeaders: true,
      disableFileHeader: !fill,
      enableGutterUtility: commentable,
      // The gutter's button only shows on hover: a touch screen taps the number.
      onLineNumberClick: commentable
        ? (line, { item }) => {
            const side =
              "annotationSide" in line ? line.annotationSide : "additions"
            const i = indexes.get(lineKey(item.id, side, line.lineNumber))
            if (i !== undefined) setEditing(i)
          }
        : undefined,
    }),
    [fill, commentable, indexes]
  )

  const set = (i: number, text?: string) => {
    const rest = comments.filter((c) => c.index !== i)
    onComment?.(text ? [...rest, { index: i, text }] : rest)
  }
  const close = () => {
    draft.current = null
    setEditing(undefined)
  }

  // Nothing the library reads as a file, such as a diff that is only Studio's notes.
  if (!files.length)
    return (
      <pre className="overflow-auto rounded-md p-2 font-mono text-caption ring-1 ring-foreground/10">
        {diff}
      </pre>
    )
  return (
    <WorkerPoolContext.Provider value={pool()}>
      <div
        className={cn(
          "diff-view min-w-0 overflow-hidden rounded-md ring-1 ring-foreground/10"
        )}
      >
        <CodeView<Note, undefined>
          ref={view}
          items={items}
          className={cn(
            "overflow-auto",
            fill ? "max-h-[70dvh]" : "max-h-[60vh]"
          )}
          options={options}
          renderGutterUtility={
            onComment
              ? (hovered, item) => (
                  <button
                    type="button"
                    aria-label="Comment on this line"
                    title="Comment on this line"
                    className="relative z-10 grid size-4 place-items-center rounded-sm bg-primary text-primary-foreground"
                    onClick={() => {
                      const h = hovered()
                      if (!h) return
                      const i = indexes.get(
                        lineKey(
                          item.id,
                          "side" in h ? String(h.side) : "additions",
                          h.lineNumber
                        )
                      )
                      if (i !== undefined) setEditing(i)
                    }}
                  >
                    <ChatCircleTextIcon className="size-3" />
                  </button>
                )
              : undefined
          }
          renderAnnotation={(...[annotation]) => {
            const note = annotation.metadata
            if (note.text !== undefined)
              return (
                <p className="px-3 py-1.5 font-sans text-xs text-amber-600 dark:text-amber-400">
                  {note.text}
                </p>
              )
            const i = note.comment
            const l = lines[i]
            const where = `${l.file}:${l.line}`
            const comment = comments.find((c) => c.index === i)
            if (editing === i) {
              if (draft.current?.index !== i)
                draft.current = {
                  index: i,
                  text: comment?.text ?? "",
                  focused: false,
                }
              const d = draft.current
              return (
                <CommentEditor
                  where={where}
                  initial={d.text}
                  focus={!d.focused}
                  onFocused={() => (d.focused = true)}
                  onChange={(text) => (d.text = text)}
                  onSave={(text) => {
                    set(i, text.trim())
                    close()
                  }}
                  onCancel={close}
                />
              )
            }
            if (!comment) return null
            return (
              <div
                role="note"
                aria-label={`Comment on ${where}`}
                className="mx-2 my-1 flex items-start gap-2 rounded-md bg-background px-2 py-1.5 font-sans text-xs whitespace-normal ring-1 ring-foreground/10"
              >
                <ChatCircleTextIcon className="mt-0.5 shrink-0 text-muted-foreground" />
                <button
                  type="button"
                  className="min-w-0 flex-1 text-left"
                  title="Edit the comment"
                  onClick={() => setEditing(i)}
                >
                  {commentLabel && (
                    <span className="block text-caption text-muted-foreground">
                      {commentLabel} · line {l.line}
                    </span>
                  )}
                  {comment.text}
                </button>
                {onComment && (
                  <Button
                    size="icon-xs"
                    variant="ghost"
                    aria-label="Remove the comment"
                    onClick={() => set(i)}
                  >
                    <XIcon />
                  </Button>
                )}
              </div>
            )
          }}
          renderCodeViewFooter={
            footer.length
              ? () => (
                  <div className="px-3 py-1.5 font-sans text-xs text-amber-600 dark:text-amber-400">
                    {footer.map((text) => (
                      <p key={text}>{text}</p>
                    ))}
                  </div>
                )
              : undefined
          }
        />
      </div>
    </WorkerPoolContext.Provider>
  )
}

/** The editor of one line's comment, holding its draft so typing renders only itself. */
function CommentEditor({
  where,
  initial,
  focus,
  onFocused,
  onChange,
  onSave,
  onCancel,
}: {
  where: string
  initial: string
  focus: boolean
  onFocused: () => void
  onChange: (text: string) => void
  onSave: (text: string) => void
  onCancel: () => void
}) {
  const [draft, setDraft] = useState(initial)
  return (
    <div className="mx-2 my-1 grid gap-1.5 rounded-md bg-background p-2 font-sans whitespace-normal ring-1 ring-foreground/10">
      <textarea
        aria-label={`Comment on ${where}`}
        autoFocus={focus}
        onFocus={onFocused}
        rows={2}
        maxLength={2000}
        value={draft}
        onChange={(e) => {
          setDraft(e.target.value)
          onChange(e.target.value)
        }}
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
