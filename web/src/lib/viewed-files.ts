import { useSyncExternalStore } from "react"

/**
 * The files of a branch the reader marked viewed, in this browser: per forge, repository and
 * branch, each path with a fingerprint of its patch. The branch's next push finds the marks,
 * and a file whose patch changed since says so instead of counting as viewed. Branches left
 * alone for two months go, and the oldest past two hundred.
 */
const prefix = "studio.viewed."
const maxAge = 60 * 24 * 60 * 60 * 1000
const maxEntries = 200

/** Path → the fingerprint of the patch it was viewed at. */
export type ViewedFiles = Readonly<Partial<Record<string, string>>>

type Entry = { files: Partial<Record<string, string>>; at: number }

/** What a branch's marks are kept under. */
export function viewedScope(
  forge: string,
  owner: string,
  repo: string,
  branch: string
) {
  return `${forge}/${owner}/${repo}#${branch.replace(/^refs\/heads\//, "")}`
}

const none: ViewedFiles = {}
// The snapshot for each scope, kept while its stored text stands, as useSyncExternalStore
// needs the same object back until something changed.
const cache = new Map<string, { raw: string | null; files: ViewedFiles }>()
const listeners = new Set<() => void>()

function read(scope: string): ViewedFiles {
  let raw: string | null = null
  try {
    raw = localStorage.getItem(prefix + scope)
  } catch {
    // storage unavailable: nothing marked
  }
  const hit = cache.get(scope)
  if (hit?.raw === raw) return hit.files
  let files: ViewedFiles = none
  try {
    if (raw) files = (JSON.parse(raw) as Partial<Entry>).files ?? none
  } catch {
    // unreadable: nothing marked
  }
  cache.set(scope, { raw, files })
  return files
}

let pruned = false

/** Drops branches older than two months, and the oldest past two hundred, once per load. */
function prune() {
  if (pruned) return
  pruned = true
  try {
    const entries: { key: string; at: number }[] = []
    for (let i = 0; i < localStorage.length; i++) {
      const key = localStorage.key(i)
      if (!key?.startsWith(prefix)) continue
      let at = 0
      try {
        at =
          (JSON.parse(localStorage.getItem(key) ?? "") as Partial<Entry>).at ??
          0
      } catch {
        // unreadable: dropped below
      }
      entries.push({ key, at })
    }
    entries.sort((a, b) => b.at - a.at)
    const now = Date.now()
    entries.forEach((e, i) => {
      if (i >= maxEntries || now - e.at > maxAge) localStorage.removeItem(e.key)
    })
  } catch {
    // storage unavailable
  }
}

/** Marks a file viewed at a patch's fingerprint, or clears its mark without one. */
export function setViewed(scope: string, path: string, patch?: string) {
  const files: Partial<Record<string, string>> = { ...read(scope) }
  if (patch === undefined) delete files[path]
  else files[path] = patch
  try {
    if (Object.keys(files).length)
      localStorage.setItem(
        prefix + scope,
        JSON.stringify({ files, at: Date.now() } satisfies Entry)
      )
    else localStorage.removeItem(prefix + scope)
  } catch {
    // storage full or unavailable: the mark is lost
  }
  prune()
  for (const l of listeners) l()
}

function subscribe(changed: () => void) {
  listeners.add(changed)
  // Another window of this browser marking a file.
  const other = (e: StorageEvent) => {
    if (!e.key || e.key.startsWith(prefix)) changed()
  }
  window.addEventListener("storage", other)
  return () => {
    listeners.delete(changed)
    window.removeEventListener("storage", other)
  }
}

/** The files marked viewed on a branch, kept up to date; none without a scope. */
export function useViewedFiles(scope: string | undefined): ViewedFiles {
  return useSyncExternalStore(subscribe, () => (scope ? read(scope) : none))
}
