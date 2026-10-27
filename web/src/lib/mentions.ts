// Mentions of workspace files in a chat message: @path, or @path:20-28 for some lines. The
// text carries them as the agent reads them; the prompt adds each as a link
// (internal/chat/prompt.go).
import type { components } from "@/lib/api/schema.gen"

export type Mention = components["schemas"]["Mention"]

/** What a mention reads as after its @: the path, quoted when it has spaces, and the lines. */
export function mentionLabel(m: Mention) {
  const path = /\s|"/.test(m.path) ? `"${m.path.replaceAll('"', "")}"` : m.path
  if (!m.start) return path
  return m.end && m.end > m.start
    ? `${path}:${m.start}-${m.end}`
    : `${path}:${m.start}`
}

export const mentionText = (m: Mention) => `@${mentionLabel(m)}`

/**
 * The @query being typed at the caret: where its @ is and what follows it, or undefined
 * when the caret isn't in one. An @ starts a mention at the start of the text or after a
 * space, so an address like a@b doesn't.
 */
export function activeQuery(text: string, caret: number) {
  const m = /(^|\s)@([^\s@"]*)$/.exec(text.slice(0, caret))
  if (!m) return undefined
  return { at: m.index + m[1].length, query: m[2] }
}

/** A query's lines, as coupon.ts:20-28 asks for them, and the rest of it. */
export function splitLines(query: string) {
  const m = /^(.*):(\d*)(?:-(\d*))?$/.exec(query)
  if (!m) return { query, start: 0, end: 0 }
  const start = Number(m[2] || 0)
  const end = Number(m[3] || 0)
  return { query: m[1], start, end: end > start ? end : 0 }
}

export type Candidate = {
  path: string
  /** The last part of the path; a folder's ends without its slash. */
  name: string
  dir: string
  folder: boolean
  /** A folder's files, at any depth. */
  files: number
  changed: boolean
  /** The span of name that matches the query, to show in bold. */
  match?: [number, number]
}

/** The index as files and folders, built once per index. */
export function candidates(paths: string[], changed: string[]) {
  const isChanged = new Set(changed)
  const folders = new Map<string, number>()
  const out: Candidate[] = []
  for (const p of paths) {
    const cut = p.lastIndexOf("/")
    out.push({
      path: p,
      name: p.slice(cut + 1),
      dir: cut < 0 ? "" : p.slice(0, cut + 1),
      folder: false,
      files: 0,
      changed: isChanged.has(p),
    })
    for (let i = p.indexOf("/"); i >= 0; i = p.indexOf("/", i + 1)) {
      const f = p.slice(0, i + 1)
      folders.set(f, (folders.get(f) ?? 0) + 1)
    }
  }
  for (const [f, files] of folders) {
    const cut = f.lastIndexOf("/", f.length - 2)
    out.push({
      path: f,
      name: f.slice(cut + 1, -1),
      dir: f.slice(0, cut + 1),
      folder: true,
      files,
      changed: false,
    })
  }
  return out
}

/**
 * The candidates for a query, best first: names that start with it, then names that contain
 * it, then paths that do; changed files first within each, then files before folders, then
 * shorter paths. A query with
 * a slash looks in that folder, and an empty one after it lists the folder. Without a query,
 * the changed files and the top of the workspace.
 */
export function rank(all: Candidate[], query: string, limit = 8) {
  const q = query.toLowerCase()
  const slash = q.lastIndexOf("/")
  const within = slash < 0 ? "" : q.slice(0, slash + 1)
  const term = q.slice(slash + 1)
  const scored: { c: Candidate; tier: number; match?: [number, number] }[] = []
  for (const c of all) {
    const path = c.path.toLowerCase()
    if (within && !path.startsWith(within)) continue
    if (path === within) continue
    if (!term) {
      // A listing: the folder's own entries, or without a folder the changed files and the
      // top level.
      const direct = c.dir.toLowerCase() === within
      if (direct) scored.push({ c, tier: c.folder ? 0 : 1 })
      else if (!within && c.changed) scored.push({ c, tier: -1 })
      continue
    }
    const name = c.name.toLowerCase()
    const at = name.indexOf(term)
    const rel = path.slice(within.length)
    if (at === 0) scored.push({ c, tier: 0, match: [0, term.length] })
    else if (at > 0) scored.push({ c, tier: 1, match: [at, at + term.length] })
    else if (rel.includes(term)) scored.push({ c, tier: 2 })
  }
  scored.sort(
    (a, b) =>
      a.tier - b.tier ||
      Number(b.c.changed) - Number(a.c.changed) ||
      Number(a.c.folder) - Number(b.c.folder) ||
      a.c.path.length - b.c.path.length ||
      a.c.path.localeCompare(b.c.path)
  )
  return {
    total: scored.length,
    shown: scored.slice(0, limit).map((s) => ({ ...s.c, match: s.match })),
  }
}

/**
 * The mentions the text still has, each once: a mention deleted from the text is no longer
 * sent.
 */
export function keptMentions(text: string, mentions: Mention[]) {
  const seen = new Set<string>()
  return mentions.filter((m) => {
    const t = mentionText(m)
    if (seen.has(t) || mentionRanges(text, [m]).length === 0) return false
    seen.add(t)
    return true
  })
}

/** Where the text has these mentions, as [start, end, mention], in order. */
export function mentionRanges(text: string, mentions: Mention[]) {
  const out: [number, number, Mention][] = []
  for (const m of mentions) {
    const t = mentionText(m)
    for (let i = text.indexOf(t); i >= 0; i = text.indexOf(t, i + t.length)) {
      const before = i === 0 ? " " : text[i - 1]
      const after = text[i + t.length] ?? " "
      // Whole words only: @a.ts isn't in @a.tsx or @a.ts:3.
      if (/\s/.test(before) && /[\s,.;!?)]/.test(after)) {
        if (!(after === "." && /[\w/]/.test(text[i + t.length + 1] ?? "")))
          out.push([i, i + t.length, m])
      }
    }
  }
  out.sort((a, b) => a[0] - b[0] || b[1] - a[1])
  // Overlaps keep the first, longest.
  const kept: typeof out = []
  for (const r of out) if (r[0] >= (kept.at(-1)?.[1] ?? 0)) kept.push(r)
  return kept
}

// Files mentions a file in the chat beside it, whose composer listens for its sandbox.
const targets = new Map<string, Set<(m: Mention) => void>>()
const watchers = new Set<() => void>()
let targetsVersion = 0

function targetsChanged() {
  targetsVersion++
  watchers.forEach((w) => w())
}

/** Takes mentions of the sandbox's files from elsewhere on the page, until the returned call. */
export function listenForMentions(sandbox: string, fn: (m: Mention) => void) {
  let set = targets.get(sandbox)
  if (!set) targets.set(sandbox, (set = new Set()))
  set.add(fn)
  targetsChanged()
  return () => {
    set.delete(fn)
    if (set.size === 0) targets.delete(sandbox)
    targetsChanged()
  }
}

/** Mentions a file in the composer that listens for its sandbox; false without one. */
export function mentionInChat(sandbox: string, m: Mention) {
  const set = targets.get(sandbox)
  set?.forEach((fn) => fn(m))
  return !!set
}

export function watchMentionTargets(fn: () => void) {
  watchers.add(fn)
  return () => {
    watchers.delete(fn)
  }
}

export const mentionTargetsVersion = () => targetsVersion

export const canMentionIn = (sandbox: string) => targets.has(sandbox)
