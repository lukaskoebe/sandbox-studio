// Highlights the files of a long diff off the page's thread (useDiffTokens in code-view.tsx).
import type { Parser } from "@lezer/common"
import { highlightLines, languageOf, loaders } from "./highlight"

export type HighlightRequest = { path: string; sides: string[] }

/**
 * A side's tokens without their text, which the page's lines already hold: a run is its length
 * and the index of its class, and line k's runs start at starts[k]. Numbers in a buffer, so a
 * large file's tokens reach the page without it unpacking thousands of objects at once.
 */
export type EncodedSide = { runs: Uint32Array; starts: Uint32Array }

/** A file's sides and the classes they name, or null for a file without a parser. */
export type HighlightReply = {
  sides: EncodedSide[]
  classes: string[]
} | null

function encode(text: string, parser: Parser, classes: string[]): EncodedSide {
  // The page keeps to its limit for what it highlights itself; here it costs nothing but time.
  const lines = highlightLines(text, parser, Infinity)
  const runs: number[] = []
  const starts = new Uint32Array(lines.length + 1)
  lines.forEach((tokens, k) => {
    starts[k] = runs.length
    for (const t of tokens) {
      let c = classes.indexOf(t.cls)
      if (c < 0) c = classes.push(t.cls) - 1
      runs.push(t.text.length, c)
    }
  })
  starts[lines.length] = runs.length
  return { runs: Uint32Array.from(runs), starts }
}

const parsers = new Map<string, Promise<Parser>>()

function parserOf(path: string) {
  const lang = languageOf(path)
  if (!lang) return undefined
  let p = parsers.get(lang)
  if (!p) parsers.set(lang, (p = loaders[lang]()))
  return p
}

self.onmessage = async ({ data }: MessageEvent<HighlightRequest>) => {
  let reply: HighlightReply = null
  try {
    const parser = await parserOf(data.path)
    if (parser) {
      const classes: string[] = []
      reply = {
        sides: data.sides.map((text) => encode(text, parser, classes)),
        classes,
      }
    }
  } catch {
    // A language that fails to load is shown plain.
  }
  const buffers = reply?.sides.flatMap((s) => [s.runs.buffer, s.starts.buffer])
  ;(self as unknown as Worker).postMessage(reply, buffers ?? [])
}
