import { useMemo } from "react"
import { ScrollArea, ScrollBar } from "@/components/ui/scroll-area"
import type { DiffLine } from "@/components/review-feedback"
import { highlightLines, useParsers, type Token } from "@/lib/highlight"

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
          <div key={i} className="flex">
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

/**
 * The tokens of each code line of a diff, by the line's index: each file's old and new
 * sides are highlighted as runs of text, so a line is colored as it reads in its file.
 */
export function useDiffTokens(lines: DiffLine[]) {
  const files = useMemo(
    () => [...new Set(lines.flatMap((l) => (l.file ? [l.file] : [])))],
    [lines]
  )
  const parserFor = useParsers(files)
  return useMemo(() => {
    const out = new Map<number, Token[]>()
    const sides = new Map<string, { old: number[]; new: number[] }>()
    lines.forEach((l, i) => {
      if (!l.file || !parserFor(l.file)) return
      let s = sides.get(l.file)
      if (!s) sides.set(l.file, (s = { old: [], new: [] }))
      if (l.text.startsWith("+")) s.new.push(i)
      else if (l.text.startsWith("-")) s.old.push(i)
      else {
        s.old.push(i)
        s.new.push(i)
      }
    })
    for (const [file, s] of sides) {
      for (const side of [s.old, s.new]) {
        const text = side.map((i) => lines[i].text.slice(1)).join("\n")
        const highlighted = highlightLines(text, parserFor(file))
        // A context line takes the new side's tokens.
        side.forEach((i, k) => {
          if (side === s.new || !out.has(i)) out.set(i, highlighted[k])
        })
      }
    }
    return out
  }, [lines, parserFor])
}
