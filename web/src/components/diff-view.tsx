import { useImperativeHandle, useMemo, useRef, useState, type Ref } from "react"
import {
  parsePatchFiles,
  registerCustomTheme,
  type CodeViewDiffItem,
  type DiffLineAnnotation,
  type FileDiffMetadata,
  type SelectedLineRange,
} from "@pierre/diffs"
import {
  CodeView,
  WorkerPoolContext,
  type CodeViewHandle,
  type CodeViewReactOptions,
} from "@pierre/diffs/react"
import { getOrCreateWorkerPoolSingleton } from "@pierre/diffs/worker"
import DiffsWorker from "@pierre/diffs/worker/worker-portable.js?worker"
import {
  ChatCircleTextIcon,
  ColumnsIcon,
  RowsIcon,
  XIcon,
} from "@phosphor-icons/react"
import { Button } from "@/components/ui/button"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { textareaClass } from "@/components/memory-dialogs"
import {
  commentWhere,
  parseDiff,
  type DiffLine,
  type LineComment,
} from "@/components/review-feedback"
import { setDiffStyle, useDiffStyle, type DiffStyle } from "@/lib/diff-style"
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

/** A file's place in the overview: what happened to it and how many lines. */
const changeLabels: Record<FileDiffMetadata["type"], [string, string]> = {
  new: ["A", "Added"],
  deleted: ["D", "Deleted"],
  change: ["M", "Modified"],
  "rename-pure": ["R", "Renamed"],
  "rename-changed": ["R", "Renamed and modified"],
}
const changeColors: Record<FileDiffMetadata["type"], string> = {
  new: "text-emerald-600 dark:text-emerald-400",
  deleted: "text-destructive",
  change: "text-sky-700 dark:text-sky-400",
  "rename-pure": "text-muted-foreground",
  "rename-changed": "text-sky-700 dark:text-sky-400",
}

function counts(f: FileDiffMetadata) {
  let added = 0
  let removed = 0
  for (const h of f.hunks) {
    added += h.additionLines
    removed += h.deletionLines
  }
  return { added, removed }
}

/** The lines a comment is being written on: the last, and the first of a range. */
type Editing = { index: number; start?: number }

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
  /** Whether a comment can cover several lines, chosen by dragging over their numbers. */
  ranges?: boolean
  /** Whether it offers stacked or split lines; full-length diffs always do. */
  layouts?: boolean
  ref?: Ref<DiffViewHandle>
}

/** A diff, windowed and highlighted in workers, with comments under its lines: stacked, or
 * side by side when full-length and asked. */
export default function DiffView({
  diff,
  comments,
  onComment,
  fill,
  commentLabel,
  ranges = true,
  layouts = fill,
  ref,
}: DiffViewProps) {
  const lines = parseDiff(diff)
  // A small diff, as in a card, stays a single column.
  const picked = useDiffStyle()
  const style = layouts ? picked : "unified"
  const [editing, setEditing] = useState<Editing>()
  const view = useRef<CodeViewHandle<Note, undefined>>(null)
  const box = useRef<HTMLDivElement>(null)
  // A line far from the view unmounts, and the editor on it: the draft outlives it.
  const draft = useRef<{
    index: number
    start?: number
    text: string
    focused: boolean
  }>(null)
  const { files, notes, footer } = useMemo(() => readPatch(lines), [lines])
  const indexes = useMemo(() => {
    const out = new Map<string, number>()
    lines.forEach((l, i) => {
      if (l.line !== undefined) out.set(lineKey(l.fileNo, sideOf(l), l.line), i)
      // A selection may start on an unchanged line's old side.
      if (l.oldLine !== undefined)
        out.set(lineKey(l.fileNo, "deletions", l.oldLine), i)
    })
    return out
  }, [lines])

  // An item keeps its version until its annotations move, so the others don't render again.
  const known = useRef(new Map<number, { key: string; version: number }>())
  // CodeView keeps an item's old data while its version stands, so the text has to count too.
  const diffKey = useMemo(() => `${diff.length}-${hash(diff)}`, [diff])
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
    if (editing !== undefined) marked.add(editing.index)
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
        diffKey +
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
  }, [diffKey, files, notes, comments, editing, lines])

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
  const selectable = commentable && ranges
  /** The lines a selection of line numbers covers, as a comment on them. */
  const rangeOf = (id: string, r: SelectedLineRange): Editing | undefined => {
    const a = indexes.get(lineKey(id, r.side ?? "additions", r.start))
    const b = indexes.get(
      lineKey(id, r.endSide ?? r.side ?? "additions", r.end)
    )
    if (a === undefined || b === undefined) {
      const one = a ?? b
      return one === undefined ? undefined : { index: one }
    }
    const [lo, hi] = a <= b ? [a, b] : [b, a]
    return lo < hi ? { index: hi, start: lo } : { index: hi }
  }
  const open = (e: Editing) => {
    const c = comments.find((x) => x.index === e.index)
    setEditing(
      e.start === undefined && c?.start !== undefined
        ? { ...e, start: c.start }
        : e
    )
  }
  const options = useMemo<CodeViewReactOptions<Note, undefined>>(
    () => ({
      theme: "studio",
      diffStyle: style,
      overflow: "scroll",
      lineDiffType: "word",
      tokenizeMaxLineLength: maxTokenLine,
      maxLineDiffLength: maxTokenLine,
      stickyHeaders: true,
      // The heights the view reserves for what it hasn't drawn yet. They must match the styles
      // (--diffs-line-height in styles.css, a 40px file header): the defaults of 20px lines
      // left a gap at the bottom of a fast scroll until the real rows came in.
      itemMetrics: { lineHeight: 16, diffHeaderHeight: 40 },
      disableFileHeader: !fill,
      enableGutterUtility: commentable,
      // A click on a number, a drag over several or a shift-click selects them, and the
      // comment covers what was selected. The gutter's button only shows on hover: a touch
      // screen taps the number.
      enableLineSelection: selectable,
      onLineSelectionEnd: selectable
        ? (
            range: SelectedLineRange | null,
            { item }: { item: { id: string } }
          ) => {
            const e = range && rangeOf(item.id, range)
            if (e) open(e)
          }
        : undefined,
      onLineNumberClick:
        commentable && !selectable
          ? (line, { item }) => {
              const side =
                "annotationSide" in line ? line.annotationSide : "additions"
              const i = indexes.get(lineKey(item.id, side, line.lineNumber))
              if (i !== undefined) open({ index: i })
            }
          : undefined,
    }),
    [fill, style, commentable, selectable, indexes, comments]
  )

  const set = (i: number, text?: string, start?: number) => {
    const rest = comments.filter((c) => c.index !== i)
    onComment?.(
      text
        ? [
            ...rest,
            start === undefined
              ? { index: i, text }
              : { index: i, text, start },
          ]
        : rest
    )
  }
  const close = () => {
    draft.current = null
    setEditing(undefined)
    view.current?.clearSelectedLines()
  }
  const jumpToFile = (fileNo: number) => {
    // The diff's own box may be partly out of the page's view, as below the overview.
    box.current?.scrollIntoView({ block: "nearest" })
    view.current?.scrollTo({
      type: "item",
      id: String(fileNo),
      align: "start",
    })
  }
  const overview = fill && files.length > 1

  // Nothing the library reads as a file, such as a diff that is only Studio's notes.
  if (!files.length)
    return (
      <pre className="overflow-auto rounded-md p-2 font-mono text-caption ring-1 ring-foreground/10">
        {diff}
      </pre>
    )
  return (
    <WorkerPoolContext.Provider value={pool()}>
      <div className="@container min-w-0">
        <div
          className={cn(
            "grid min-w-0 gap-2",
            overview && "@3xl:grid-cols-[minmax(12rem,16rem)_minmax(0,1fr)]"
          )}
        >
          {overview && <FileOverview files={files} onPick={jumpToFile} />}
          <div className="grid min-w-0 content-start gap-1.5">
            {layouts && <StyleSwitch style={style} />}
            <div
              ref={box}
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
                        <CommentButton
                          hovered={hovered}
                          onPick={(range) => {
                            const e = rangeOf(item.id, range)
                            if (!e) return
                            open(e)
                            // What the comment covers stays marked while it's written.
                            if (selectable)
                              view.current?.setSelectedLines({
                                id: item.id,
                                range,
                              })
                          }}
                          onDrag={
                            selectable
                              ? (range) =>
                                  view.current?.setSelectedLines({
                                    id: item.id,
                                    range,
                                  })
                              : undefined
                          }
                          selection={() => {
                            const sel = selectable
                              ? view.current?.getSelectedLines()
                              : null
                            return sel?.id === item.id ? sel.range : undefined
                          }}
                        />
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
                  const comment = comments.find((c) => c.index === i)
                  if (editing?.index === i) {
                    if (
                      draft.current?.index !== i ||
                      draft.current.start !== editing.start
                    )
                      draft.current = {
                        index: i,
                        start: editing.start,
                        text:
                          draft.current?.index === i
                            ? draft.current.text
                            : (comment?.text ?? ""),
                        focused: false,
                      }
                    const d = draft.current
                    const where = commentWhere(lines, {
                      index: i,
                      start: d.start,
                      text: "",
                    })
                    return (
                      <CommentEditor
                        where={where}
                        initial={d.text}
                        focus={!d.focused}
                        onFocused={() => (d.focused = true)}
                        onChange={(text) => (d.text = text)}
                        onSave={(text) => {
                          set(i, text.trim(), d.start)
                          close()
                        }}
                        onCancel={close}
                      />
                    )
                  }
                  if (!comment) return null
                  const where = commentWhere(lines, comment)
                  const l = lines[i]
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
                        onClick={() => open({ index: i, start: comment.start })}
                      >
                        {(commentLabel || comment.start !== undefined) && (
                          <span className="block text-caption text-muted-foreground">
                            {[
                              commentLabel,
                              comment.start !== undefined
                                ? `lines ${lines[comment.start]?.line}–${l.line}`
                                : `line ${l.line}`,
                            ]
                              .filter(Boolean)
                              .join(" · ")}
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
          </div>
        </div>
      </div>
    </WorkerPoolContext.Provider>
  )
}

/** Stacked lines or old and new side by side, for every full-length diff in this browser. */
function StyleSwitch({ style }: { style: DiffStyle }) {
  return (
    <ToggleGroup
      aria-label="Diff layout"
      variant="outline"
      size="sm"
      spacing={0}
      className="justify-self-end"
      value={[style]}
      onValueChange={(v) => {
        if (v[0] === "unified" || v[0] === "split") setDiffStyle(v[0])
      }}
    >
      <ToggleGroupItem value="unified" title="Old and new lines in one column">
        <RowsIcon />
        Stacked
      </ToggleGroupItem>
      <ToggleGroupItem
        value="split"
        title="Old lines on the left, new on the right"
      >
        <ColumnsIcon />
        Split
      </ToggleGroupItem>
    </ToggleGroup>
  )
}

/** The diff's files with what happened to each and their lines, jumping to one on a click. */
function FileOverview({
  files,
  onPick,
}: {
  files: FileDiffMetadata[]
  onPick: (fileNo: number) => void
}) {
  const all = files.map(counts)
  const added = all.reduce((n, c) => n + c.added, 0)
  const removed = all.reduce((n, c) => n + c.removed, 0)
  return (
    <nav
      aria-label="Files in the diff"
      className="grid max-h-40 min-w-0 content-start overflow-auto rounded-md py-1 ring-1 ring-foreground/10 @3xl:max-h-[70dvh]"
    >
      <p className="flex h-6 items-center gap-2 px-2.5 section-label text-muted-foreground">
        Files
        <span className="ml-auto font-mono text-caption tracking-normal normal-case">
          {files.length} ·{" "}
          <span className="text-emerald-600 dark:text-emerald-400">
            +{added}
          </span>{" "}
          <span className="text-destructive">−{removed}</span>
        </span>
      </p>
      <ul className="grid min-w-0">
        {files.map((f, i) => {
          const [mark, label] = changeLabels[f.type]
          const dir = f.name.includes("/")
            ? f.name.slice(0, f.name.lastIndexOf("/") + 1)
            : ""
          const base = f.name.slice(dir.length)
          return (
            <li key={i} className="min-w-0">
              <button
                type="button"
                title={f.prevName ? `${f.prevName} → ${f.name}` : f.name}
                className="flex min-h-6 w-full min-w-0 items-center gap-2 px-2.5 py-0.5 text-left hover:bg-muted focus-visible:bg-muted focus-visible:outline-none"
                onClick={() => onPick(i)}
              >
                <span
                  aria-label={label}
                  className={cn(
                    "w-3 shrink-0 text-center font-mono text-caption font-semibold",
                    changeColors[f.type]
                  )}
                >
                  {mark}
                </span>
                <span
                  className="min-w-0 flex-1 truncate font-mono text-caption"
                  dir="rtl"
                >
                  {/* Right-to-left keeps the file name visible when a long path is cut. */}
                  <bdi>
                    <span className="text-muted-foreground">{dir}</span>
                    {base}
                  </bdi>
                </span>
                <span className="shrink-0 font-mono text-caption">
                  {all[i].added > 0 && (
                    <span className="text-emerald-600 dark:text-emerald-400">
                      +{all[i].added}
                    </span>
                  )}{" "}
                  {all[i].removed > 0 && (
                    <span className="text-destructive">−{all[i].removed}</span>
                  )}
                </span>
              </button>
            </li>
          )
        })}
      </ul>
    </nav>
  )
}

type Hovered = () =>
  { lineNumber: number; side?: unknown; annotationSide?: unknown } | undefined

const pointOf = (h: NonNullable<ReturnType<Hovered>>) => ({
  line: h.lineNumber,
  side:
    h.side === "deletions" ? ("deletions" as const) : ("additions" as const),
})

/**
 * The line under a point of a file's diff, which draws its lines in a shadow root: the
 * library's hovered line stays put while the pointer is over this button.
 */
function lineAt(x: number, y: number, inside: HTMLElement) {
  let host = inside.parentElement
  while (host && !host.shadowRoot) host = host.parentElement
  const hit = host?.shadowRoot
    ?.elementsFromPoint(x, y)
    .find(
      (n): n is HTMLElement =>
        n instanceof HTMLElement &&
        n.dataset.lineIndex !== undefined &&
        (n.dataset.columnNumber ?? n.dataset.line) !== undefined
    )
  if (!hit) return undefined
  return {
    line: Number(hit.dataset.columnNumber ?? hit.dataset.line),
    // An unchanged line in a split diff's old column is on the old side.
    side:
      hit.dataset.lineType?.includes("deletion") ||
      (!hit.dataset.lineType?.includes("addition") &&
        hit.closest("[data-deletions]"))
        ? ("deletions" as const)
        : ("additions" as const),
  }
}

/**
 * The button at the start of the hovered line. A click comments on the line, or on the
 * selection it is on; pressing it and dragging over more lines comments on all of them.
 */
function CommentButton({
  hovered,
  onPick,
  onDrag,
  selection,
}: {
  hovered: Hovered
  onPick: (range: SelectedLineRange) => void
  onDrag?: (range: SelectedLineRange) => void
  selection: () => SelectedLineRange | undefined
}) {
  const range = (
    a: ReturnType<typeof pointOf>,
    b: ReturnType<typeof pointOf>
  ): SelectedLineRange => ({
    start: a.line,
    side: a.side,
    end: b.line,
    endSide: b.side,
  })
  /** A click on a line of the selection takes the selection. */
  const pick = (at: ReturnType<typeof pointOf>, moved: boolean) => {
    const sel = selection()
    if (!moved && sel) {
      const lo = Math.min(sel.start, sel.end)
      const hi = Math.max(sel.start, sel.end)
      if (at.line >= lo && at.line <= hi) return onPick(sel)
    }
    onPick(range(at, at))
  }
  return (
    <button
      type="button"
      aria-label="Comment on this line"
      title="Comment on this line, or drag over several"
      className="relative z-10 grid size-4 touch-none place-items-center rounded-sm bg-primary text-primary-foreground"
      onPointerDown={(e) => {
        const h = hovered()
        if (e.button !== 0 || !h || !onDrag) return
        e.preventDefault()
        const at = pointOf(h)
        // Shift extends the selection to here, as on a line's number.
        const sel = selection()
        if (e.shiftKey && sel) {
          onPick({ ...sel, end: at.line, endSide: at.side })
          return
        }
        const anchor = at
        let last = anchor
        const button = e.currentTarget
        const move = (ev: PointerEvent) => {
          const now = lineAt(ev.clientX, ev.clientY, button)
          if (!now) return
          last = now
          if (last.line !== anchor.line || last.side !== anchor.side)
            onDrag(range(anchor, last))
        }
        const up = (ev: PointerEvent) => {
          last = lineAt(ev.clientX, ev.clientY, button) ?? last
          document.removeEventListener("pointermove", move)
          document.removeEventListener("pointerup", up)
          const moved = last.line !== anchor.line || last.side !== anchor.side
          if (moved) onPick(range(anchor, last))
          else pick(anchor, false)
        }
        document.addEventListener("pointermove", move)
        document.addEventListener("pointerup", up)
      }}
      onClick={(e) => {
        // A pointer's press was handled above; this is the keyboard, or no dragging.
        if (e.detail !== 0 && onDrag) return
        const h = hovered()
        if (h) pick(pointOf(h), false)
      }}
    >
      <ChatCircleTextIcon className="size-3" />
    </button>
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
