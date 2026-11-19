import {
  useEffect,
  useImperativeHandle,
  useMemo,
  useRef,
  useState,
  type ReactNode,
  type Ref,
} from "react"
import {
  parsePatchFiles,
  registerCustomTheme,
  type CodeView as CodeViewInstance,
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
import { ChatCircleTextIcon, XIcon } from "@phosphor-icons/react"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { DiffStyleSwitch } from "@/components/diff-style-switch"
import { textareaClass } from "@/components/memory-dialogs"
import {
  commentWhere,
  DiffFrameText,
  parseDiff,
  type DiffLine,
  type LineComment,
  type ViewedProgress,
} from "@/components/review-feedback"
import { useDiffStyle } from "@/lib/diff-style"
import type { DiffPlace } from "@/lib/review-drafts"
import { cn } from "@/lib/utils"
import { useTextSize } from "@/lib/theme"
import { setViewed, useViewedFiles } from "@/lib/viewed-files"

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

/** How much of a diff gets its colours before it scrolls into view: enough for a large review,
 * while a huge one leaves the workers' cache of 100 files room for what's on screen. */
const primeFiles = 60
const primeLines = 20_000

/**
 * Starts colouring a diff's files as it opens, one at a time, so a file on screen waits for at
 * most one of them. A file is drawn plain until its colours arrive, which on a first fast
 * scroll through a large review left whole screens uncoloured for over a second.
 */
function usePrimedColors(files: FileDiffMetadata[]) {
  useEffect(() => {
    const workers = pool()
    const done = new AbortController()
    void (async () => {
      let budget = primeLines
      for (const file of files.slice(0, primeFiles)) {
        budget -= file.unifiedLineCount
        if (done.signal.aborted || budget < 0 || !workers.isWorkingPool())
          return
        // A failed file colours when it shows, as before.
        await workers.primeDiffHighlightCache(file).catch(() => {})
      }
    })()
    return () => done.abort()
  }, [files])
}

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

/** A fingerprint of each file's patch, which a viewed mark holds until the file changes. */
function patchHashes(lines: DiffLine[], count: number) {
  const out = new Array<number>(count).fill(0x811c9dc5)
  for (const l of lines) {
    if (l.fileNo < 0 || l.fileNo >= count) continue
    let h = out[l.fileNo]
    for (let i = 0; i < l.text.length; i++) {
      h ^= l.text.charCodeAt(i)
      h = Math.imul(h, 0x01000193)
    }
    h ^= 0x0a
    out[l.fileNo] = Math.imul(h, 0x01000193)
  }
  return out.map((h) => (h >>> 0).toString(36))
}

/** Where a file stands with the reader: not marked, marked at this patch, or marked before
 * it changed. */
type Seen = "unviewed" | "viewed" | "changed"

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
  /** Brings the next file not marked viewed into view, after the one being read. */
  nextUnviewed: () => void
}

export type DiffViewProps = {
  diff: string
  comments: LineComment[]
  onComment?: (comments: LineComment[]) => void
  /**
   * Fills its parent's height as the review's one scroll frame: each file's name on it, the
   * file overview beside it once there is room, and no switch of its own, as the review's bar
   * holds it.
   */
  fill?: boolean
  /** What scrolls in above the first file, such as the review's head, when it fills. */
  header?: ReactNode
  /** What scrolls in after the last file, when it fills. */
  after?: ReactNode
  commentLabel?: string
  /** Whether a comment can cover several lines, chosen by dragging over their numbers. */
  ranges?: boolean
  /** Whether it offers stacked or split lines; full-length diffs always do. */
  layouts?: boolean
  /** Where to open: a file by its path, and how far into it, as onPlace reported. */
  place?: DiffPlace
  /** Says where the reader is as they scroll, a moment after they stop. */
  onPlace?: (place: DiffPlace) => void
  /**
   * Offers a Viewed mark on each file, kept for a branch (viewedScope): a viewed file folds to
   * its name, and one whose patch changed since its mark says so and counts as not viewed.
   */
  viewed?: string
  /** How many of the files are viewed, as that changes. */
  onViewed?: (progress: ViewedProgress) => void
  ref?: Ref<DiffViewHandle>
}

/** A diff, windowed and highlighted in workers, with comments under its lines: stacked, or
 * side by side when full-length and asked. */
export default function DiffView({
  diff,
  comments,
  onComment,
  fill,
  header,
  after,
  commentLabel,
  ranges = true,
  layouts = fill,
  place,
  onPlace,
  viewed,
  onViewed,
  ref,
}: DiffViewProps) {
  const lines = parseDiff(diff)
  // A small diff, as in a card, stays a single column.
  const picked = useDiffStyle()
  const style = layouts ? picked : "unified"
  const [editing, setEditing] = useState<Editing>()
  const view = useRef<CodeViewHandle<Note, undefined>>(null)
  // Where the reader is: the file at the top of the view and how far into it. Kept as they
  // scroll, while the view shows; a hidden view's box has no scroll position to read.
  const here = useRef<DiffPlace | undefined>(place)
  const box = useRef<HTMLDivElement>(null)
  // A line far from the view unmounts, and the editor on it: the draft outlives it.
  const draft = useRef<{
    index: number
    start?: number
    text: string
    focused: boolean
  }>(null)
  const { files, notes, footer } = useMemo(() => readPatch(lines), [lines])
  usePrimedColors(files)
  const patches = useMemo(
    () => patchHashes(lines, files.length),
    [lines, files.length]
  )
  const marks = useViewedFiles(viewed)
  const seen = useMemo(
    () =>
      files.map((f, i): Seen => {
        const m = marks[f.name]
        if (m === undefined) return "unviewed"
        return m === patches[i] ? "viewed" : "changed"
      }),
    [files, marks, patches]
  )
  // Viewed files the reader opened again, which stay marked.
  const [opened, setOpened] = useState<ReadonlySet<string>>(() => new Set())
  const folded = (i: number) =>
    seen[i] === "viewed" && !opened.has(files[i].name)
  const progress = useMemo(
    () => ({
      viewed: seen.filter((s) => s === "viewed").length,
      total: seen.length,
    }),
    [seen]
  )
  useEffect(() => {
    if (viewed) onViewed?.(progress)
  }, [viewed, onViewed, progress])
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
      const collapsed = seen[fileNo] === "viewed" && !opened.has(fileDiff.name)
      // The header's mark is drawn from the item, so a new mark is a new version.
      const key =
        diffKey +
        ":" +
        seen[fileNo] +
        (collapsed ? ":folded:" : ":") +
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
        collapsed,
      }
    })
  }, [diffKey, files, notes, comments, editing, lines, seen, opened])

  useImperativeHandle(
    ref,
    () => ({
      scrollToLine: (i) => {
        const l = lines[i]
        if (l.line === undefined) return
        // A jump as the view shows again goes where it was asked, not back to the place.
        here.current = undefined
        view.current?.scrollTo({
          type: "line",
          id: String(l.fileNo),
          lineNumber: l.line,
          side: sideOf(l),
          align: "center",
        })
      },
      nextUnviewed: () => {
        const at = here.current
          ? files.findIndex((f) => f.name === here.current?.file)
          : -1
        // Above the first file, the first is next; in a file, the one after it.
        const from = here.current && here.current.offset >= 0 ? at + 1 : 0
        for (let k = 0; k < files.length; k++) {
          const i = (from + k) % files.length
          if (seen[i] !== "viewed") return jumpToFile(i)
        }
      },
    }),
    [lines, files, seen]
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
  // The rows grow with the text size picked in Settings, as --diffs-line-height does.
  const scale = useTextSize() / 100
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
      // (--diffs-line-height in styles.css, a 40px file header at 100%): the defaults of 20px
      // lines left a gap at the bottom of a fast scroll until the real rows came in.
      itemMetrics: {
        lineHeight: Math.round(16 * scale),
        diffHeaderHeight: Math.round(40 * scale),
      },
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
    [fill, style, commentable, selectable, indexes, comments, scale]
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
  function jumpToFile(fileNo: number) {
    // A folded file opens where it is picked, and keeps its mark.
    if (folded(fileNo)) setOpened((o) => new Set(o).add(files[fileNo].name))
    view.current?.scrollTo({
      type: "item",
      id: String(fileNo),
      align: "start",
    })
  }
  // A file marked from inside it folds with the view at its name, the next file below.
  const refold = useRef<number>(undefined)
  useEffect(() => {
    const i = refold.current
    if (i === undefined) return
    refold.current = undefined
    view.current?.scrollTo({ type: "item", id: String(i), align: "start" })
  }, [items])
  const mark = (fileNo: number, on: boolean) => {
    if (!viewed) return
    const name = files[fileNo].name
    setViewed(viewed, name, on ? patches[fileNo] : undefined)
    setOpened((o) => {
      if (!o.has(name)) return o
      const next = new Set(o)
      next.delete(name)
      return next
    })
    if (on && here.current?.file === name && here.current.offset > 0)
      refold.current = fileNo
  }
  const toggleFold = (fileNo: number) => {
    const name = files[fileNo].name
    setOpened((o) => {
      const next = new Set(o)
      if (!next.delete(name)) next.add(name)
      return next
    })
  }
  const overview = fill && files.length > 1

  const goTo = (p: DiffPlace) => {
    const i = files.findIndex((f) => f.name === p.file)
    if (i < 0) return false
    // An item's offset counts up the page; a place counts down into the file.
    view.current?.scrollTo({
      type: "item",
      id: String(i),
      align: "start",
      offset: -p.offset,
    })
    return true
  }
  // Back where the reader was: once, when the view first has the files.
  const placed = useRef(false)
  useEffect(() => {
    if (placed.current || !place) return
    if (goTo(place)) placed.current = true
  })
  // Hidden behind another tab and shown again, the box starts at the top: back to the place.
  const back = useRef(goTo)
  back.current = goTo
  const hasFiles = files.length > 0
  useEffect(() => {
    const el = box.current
    if (!el) return
    let shown = el.offsetHeight > 0
    const watch = new ResizeObserver(() => {
      const now = el.offsetHeight > 0
      if (now && !shown && here.current) back.current(here.current)
      shown = now
    })
    watch.observe(el)
    return () => watch.disconnect()
  }, [hasFiles])
  const reporting = useRef<number>(undefined)
  useEffect(() => () => window.clearTimeout(reporting.current), [])
  const track = (top: number, viewer: CodeViewInstance<Note, undefined>) => {
    if (!box.current?.offsetHeight || !files.length) return
    // The last file that starts at or above the top of the view.
    let lo = 0
    let hi = files.length - 1
    let at = 0
    while (lo <= hi) {
      const mid = (lo + hi) >> 1
      if ((viewer.getTopForItem(String(mid)) ?? 0) <= top) {
        at = mid
        lo = mid + 1
      } else hi = mid - 1
    }
    // Above the first file, in what scrolls in before it, the offset is less than 0.
    const p = {
      file: files[at].name,
      offset: Math.round(top - (viewer.getTopForItem(String(at)) ?? 0)),
    }
    here.current = p
    if (!onPlace) return
    window.clearTimeout(reporting.current)
    reporting.current = window.setTimeout(() => onPlace(p), 250)
  }

  // Nothing the library reads as a file, such as a diff that is only Studio's notes.
  if (!files.length)
    return fill ? (
      <DiffFrameText header={header} after={after}>
        <pre className="overflow-auto font-mono text-caption">{diff}</pre>
      </DiffFrameText>
    ) : (
      <pre className="overflow-auto rounded-md p-2 font-mono text-caption ring-1 ring-foreground/10">
        {diff}
      </pre>
    )
  return (
    <WorkerPoolContext.Provider value={pool()}>
      <div
        className={cn(
          "@container min-w-0",
          fill && "flex min-h-0 flex-1 flex-col"
        )}
      >
        <div
          className={cn("min-w-0 gap-2", fill ? "flex min-h-0 flex-1" : "grid")}
        >
          {/* Beside the diff once there is room; in a narrow one it scrolls in with the head. */}
          {overview && (
            <FileOverview
              files={files}
              onPick={jumpToFile}
              seen={viewed ? seen : undefined}
              onMark={mark}
              className="hidden w-64 shrink-0 @3xl:grid"
            />
          )}
          <div
            className={cn(
              "min-w-0 gap-1.5",
              fill ? "flex min-h-0 flex-1 flex-col" : "grid content-start"
            )}
          >
            {layouts && !fill && <DiffStyleSwitch />}
            <div
              ref={box}
              className={cn(
                "diff-view min-w-0 overflow-hidden rounded-md ring-1 ring-foreground/10",
                fill && "flex min-h-0 flex-1 flex-col"
              )}
            >
              <CodeView<Note, undefined>
                ref={view}
                items={items}
                onScroll={track}
                className={cn(
                  "overflow-auto",
                  fill ? "min-h-0 flex-1" : "max-h-[60vh]"
                )}
                options={options}
                renderCodeViewHeader={
                  fill
                    ? () => (
                        <div className="grid min-w-0 gap-3 p-3 font-sans text-xs whitespace-normal">
                          {header}
                          {overview && (
                            <FileOverview
                              files={files}
                              onPick={jumpToFile}
                              seen={viewed ? seen : undefined}
                              onMark={mark}
                              className="max-h-40 @3xl:hidden"
                            />
                          )}
                        </div>
                      )
                    : undefined
                }
                renderHeaderMetadata={
                  viewed
                    ? (item) => {
                        const i = Number(item.id)
                        return (
                          <ViewedMark
                            name={files[i].name}
                            seen={seen[i]}
                            folded={folded(i)}
                            onMark={(on) => mark(i, on)}
                            onFold={() => toggleFold(i)}
                          />
                        )
                      }
                    : undefined
                }
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
                  footer.length || (fill && after)
                    ? () => (
                        <div className="grid min-w-0 gap-3 font-sans text-xs whitespace-normal">
                          {footer.length > 0 && (
                            <div className="px-3 py-1.5 text-amber-600 dark:text-amber-400">
                              {footer.map((text) => (
                                <p key={text}>{text}</p>
                              ))}
                            </div>
                          )}
                          {fill && after && <div className="p-3">{after}</div>}
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

/** The diff's files with what happened to each and their lines, jumping to one on a click. */
function FileOverview({
  files,
  onPick,
  seen,
  onMark,
  className,
}: {
  files: FileDiffMetadata[]
  onPick: (fileNo: number) => void
  /** Each file's viewed mark, when the diff offers them. */
  seen?: Seen[]
  onMark?: (fileNo: number, on: boolean) => void
  className?: string
}) {
  const all = files.map(counts)
  const added = all.reduce((n, c) => n + c.added, 0)
  const removed = all.reduce((n, c) => n + c.removed, 0)
  return (
    <nav
      aria-label="Files in the diff"
      className={cn(
        "grid min-w-0 content-start overflow-auto rounded-md py-1 ring-1 ring-foreground/10",
        className
      )}
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
            <li
              key={i}
              className={cn(
                "flex min-w-0 items-center hover:bg-muted",
                seen && "pl-2.5"
              )}
            >
              {seen && (
                <Checkbox
                  aria-label={`Viewed ${f.name}`}
                  checked={seen[i] === "viewed"}
                  onCheckedChange={(on) => onMark?.(i, on)}
                  className="size-3.5"
                />
              )}
              <button
                type="button"
                title={f.prevName ? `${f.prevName} → ${f.name}` : f.name}
                className={cn(
                  "flex min-h-6 w-full min-w-0 items-center gap-2 px-2.5 py-0.5 text-left focus-visible:bg-muted focus-visible:outline-none",
                  seen?.[i] === "viewed" && "opacity-60"
                )}
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
                {seen?.[i] === "changed" && (
                  <span
                    title="Changed since you viewed it"
                    className="size-1.5 shrink-0 rounded-full bg-amber-500"
                  >
                    <span className="sr-only">Changed since you viewed it</span>
                  </span>
                )}
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

/** A file header's Viewed mark, and the way to open a viewed file again without unmarking it. */
function ViewedMark({
  name,
  seen,
  folded,
  onMark,
  onFold,
}: {
  name: string
  seen: Seen
  folded: boolean
  onMark: (on: boolean) => void
  onFold: () => void
}) {
  return (
    <span
      data-testid="viewed-mark"
      className="flex items-center gap-3 font-sans text-xs whitespace-nowrap text-muted-foreground"
    >
      {seen === "changed" && (
        <span className="text-amber-600 dark:text-amber-400">
          <span className="@lg:hidden">Changed</span>
          <span className="hidden @lg:inline">Changed since you viewed it</span>
        </span>
      )}
      {seen === "viewed" && (
        <button
          type="button"
          className="hover:text-foreground"
          aria-label={`${folded ? "Show" : "Fold"} ${name}`}
          onClick={onFold}
        >
          {folded ? "Show" : "Fold"}
        </button>
      )}
      <label className="flex cursor-pointer items-center gap-1.5 hover:text-foreground">
        <Checkbox
          aria-label={`Viewed ${name}`}
          checked={seen === "viewed"}
          onCheckedChange={onMark}
          className="size-3.5"
        />
        Viewed
      </label>
    </span>
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
