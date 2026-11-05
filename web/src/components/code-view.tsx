import { useEffect, useMemo, useState } from "react"
import { ScrollArea, ScrollBar } from "@/components/ui/scroll-area"
import type { DiffLine as FullLine } from "@/components/review-feedback"
import { highlightLines, languageOf, type Token } from "@/lib/highlight"
import type {
  EncodedSide,
  HighlightReply,
  HighlightRequest,
} from "@/lib/highlight.worker"
import { useParsers } from "@/lib/use-parsers"

/** A line's tokens as spans. */
export function Tokens({ tokens }: { tokens: Token[] }) {
  return tokens.map((t, i) =>
    t.cls ? (
      <span key={i} className={t.cls}>
        {t.text}
      </span>
    ) : (
      t.text
    )
  )
}

/** A text file, highlighted by its language, with line numbers. */
export function CodeView({ path, text }: { path: string; text: string }) {
  const parser = useParsers([path])(path)
  const lines = useMemo(() => {
    const out = highlightLines(text, parser)
    // A final newline ends the last line rather than starting one.
    if (out.length > 1 && out.at(-1)?.every((t) => !t.text)) out.pop()
    return out
  }, [text, parser])
  const gutter = `${String(lines.length).length + 1}ch`
  return (
    <ScrollArea
      className="rounded-md bg-muted/40 ring-1 ring-foreground/10"
      viewportClassName="max-h-[70vh]"
    >
      <pre
        data-language={parser ? "highlighted" : undefined}
        className="py-2 font-mono text-caption/[1.45]"
      >
        {lines.map((l, i) => (
          <div key={i} data-line={i + 1} className="flex">
            <span
              aria-hidden
              className="shrink-0 pr-3 pl-2 text-right text-muted-foreground/60 select-none"
              style={{ width: `calc(${gutter} + 1.25rem)` }}
            >
              {i + 1}
            </span>
            <span className="pr-2 whitespace-pre">
              <Tokens tokens={l} />
            </span>
          </div>
        ))}
      </pre>
      <ScrollBar orientation="horizontal" />
    </ScrollArea>
  )
}

/** A diff line's tokens by the line's index, or undefined for a line shown plain. */
export type LineTokens = { get: (i: number) => Token[] | undefined }

const noTokens: LineTokens = { get: () => undefined }

/** A diff longer than this highlights in a worker: a long one would hold up the page. */
const workerLines = 600
/** A longer line shows plain: as a minified file's would, its tokens make thousands of elements. */
const maxTokenLine = 2000
/** How often a worker's results show, in ms: each time the lines near the view render again. */
const showEvery = 200

/** The indexes of each file's lines on its old and new sides, so a side reads as its file does. */
type DiffLine = Pick<FullLine, "text" | "file">

function fileSides(lines: DiffLine[]) {
  const sides = new Map<string, { old: number[]; new: number[] }>()
  lines.forEach((l, i) => {
    if (!l.file) return
    let s = sides.get(l.file)
    if (!s) sides.set(l.file, (s = { old: [], new: [] }))
    if (l.text.startsWith("+")) s.new.push(i)
    else if (l.text.startsWith("-")) s.old.push(i)
    else {
      s.old.push(i)
      s.new.push(i)
    }
  })
  return sides
}

const tooLong = (l: DiffLine) => l.text.length > maxTokenLine

function sideText(lines: DiffLine[], side: number[]) {
  return side
    .map((i) => (tooLong(lines[i]) ? "" : lines[i].text.slice(1)))
    .join("\n")
}

/** The tokens of a side's line k, from the worker's runs and the line's own text. */
function runsOf(line: string, side: EncodedSide, k: number, classes: string[]) {
  const out: Token[] = []
  let at = 0
  for (let r = side.starts[k]; r < side.starts[k + 1]; r += 2) {
    const end = at + side.runs[r]
    out.push({ text: line.slice(at, end), cls: classes[side.runs[r + 1]] })
    at = end
  }
  return out
}

/** The tokens of a long diff as a worker finds them, a file at a time: none until then. */
function useWorkerTokens(lines: DiffLine[] | undefined): LineTokens {
  const [shown, setShown] = useState<{
    lines: DiffLine[]
    tokens: LineTokens
  }>()
  useEffect(() => {
    if (!lines) return
    const files = [...fileSides(lines)].filter(([file]) => languageOf(file))
    if (files.length === 0) return
    let worker: Worker
    try {
      worker = new Worker(
        new URL("../lib/highlight.worker.ts", import.meta.url),
        { type: "module" }
      )
    } catch {
      return // The diff stays plain.
    }
    // Where each line's tokens are: which file and side, and its line there.
    const found: { side: EncodedSide; classes: string[] }[] = []
    const sideOf = new Int32Array(lines.length).fill(-1)
    const lineOf = new Int32Array(lines.length)
    const get = (i: number) => {
      const f = found[sideOf[i]] as (typeof found)[number] | undefined
      return f && runsOf(lines[i].text.slice(1), f.side, lineOf[i], f.classes)
    }
    let last = 0
    const show = () => {
      last = performance.now()
      setShown({ lines, tokens: { get } })
    }
    // One file at a time, and a page that moves on ends the worker in the middle of one.
    let next = 0
    const send = () => {
      if (next === files.length) {
        worker.terminate()
        show()
        return
      }
      const [path, s] = files[next]
      worker.postMessage({
        path,
        sides: [sideText(lines, s.old), sideText(lines, s.new)],
      } satisfies HighlightRequest)
    }
    worker.onmessage = ({ data }: MessageEvent<HighlightReply>) => {
      const [, s] = files[next++]
      if (data) {
        // A context line takes the new side's tokens.
        ;[s.old, s.new].forEach((indexes, n) => {
          found.push({ side: data.sides[n], classes: data.classes })
          indexes.forEach((i, k) => {
            if (tooLong(lines[i])) return
            sideOf[i] = found.length - 1
            lineOf[i] = k
          })
        })
      }
      if (performance.now() - last > showEvery) show()
      send()
    }
    worker.onerror = () => worker.terminate()
    send()
    return () => worker.terminate()
  }, [lines])
  return shown && shown.lines === lines ? shown.tokens : noTokens
}

/**
 * The tokens of each code line of a diff, by the line's index: each file's old and new
 * sides are highlighted as runs of text, so a line is colored as it reads in its file. A long
 * diff shows plain until a worker has them, so the page stays responsive.
 */
export function useDiffTokens(lines: DiffLine[]): LineTokens {
  const long = lines.length > workerLines
  const files = useMemo(
    () =>
      long ? [] : [...new Set(lines.flatMap((l) => (l.file ? [l.file] : [])))],
    [lines, long]
  )
  const parserFor = useParsers(files)
  const direct = useMemo(() => {
    if (long) return noTokens
    const out = new Map<number, Token[]>()
    for (const [file, s] of fileSides(lines)) {
      const parser = parserFor(file)
      if (!parser) continue
      for (const side of [s.old, s.new]) {
        const highlighted = highlightLines(sideText(lines, side), parser)
        // A context line takes the new side's tokens.
        side.forEach((i, k) => {
          if (!tooLong(lines[i]) && (side === s.new || !out.has(i)))
            out.set(i, highlighted[k])
        })
      }
    }
    return out
  }, [lines, long, parserFor])
  const fromWorker = useWorkerTokens(long ? lines : undefined)
  return long ? fromWorker : direct
}
