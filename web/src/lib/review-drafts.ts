import { useEffect, useRef, useState } from "react"

/**
 * Per review, in this browser: what the reader wrote but hasn't sent (the note, line comments,
 * where it goes), the tab and where in the diff they were reading. A tab switch, a folded
 * screen, another computer or a reload finds it all again. Sending the review drops its entry;
 * entries left alone go after a month, and the oldest go past a hundred.
 */
const prefix = "studio.review."
const maxAge = 30 * 24 * 60 * 60 * 1000
const maxEntries = 100

/** Where in a diff the reader was: the file at the top of the view and how far into it. */
export type DiffPlace = { file: string; offset: number }

export type SavedReview<TDraft> = {
  /** What the review's draft was made from; a different one ignores the saved draft. */
  base?: string
  draft?: TDraft
  tab?: string
  place?: DiffPlace
  at: number
}

function read<TDraft>(id: string): SavedReview<TDraft> | undefined {
  try {
    const raw = localStorage.getItem(prefix + id)
    return raw ? (JSON.parse(raw) as SavedReview<TDraft>) : undefined
  } catch {
    return undefined
  }
}

let pruned = false

/** Drops entries older than a month, and the oldest past a hundred, once per page load. */
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
          (JSON.parse(localStorage.getItem(key) ?? "") as { at?: number }).at ??
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

function write<TDraft>(id: string, patch: Partial<SavedReview<TDraft>>) {
  try {
    const next = { ...read<TDraft>(id), ...patch, at: Date.now() }
    localStorage.setItem(prefix + id, JSON.stringify(next))
  } catch {
    // storage full or unavailable: the review works as before, unsaved
  }
  prune()
}

/** Forgets a review's saved draft and place, as once it is sent. */
export function dropReview(id: string) {
  try {
    localStorage.removeItem(prefix + id)
  } catch {
    // storage unavailable
  }
}

/** A short fingerprint of what a draft was made from, such as the diff its comments sit on. */
export function draftBase(...parts: (string | undefined)[]) {
  let h = 0x811c9dc5
  for (const part of parts) {
    const text = part ?? ""
    for (let i = 0; i < text.length; i++) {
      h ^= text.charCodeAt(i)
      h = Math.imul(h, 0x01000193)
    }
    h ^= 0xff
    h = Math.imul(h, 0x01000193)
  }
  return (h >>> 0).toString(36)
}

/**
 * A review's draft, starting from what was saved for it, and saved again as it changes. A
 * draft saved from a different `base` (the diff changed under it) goes through `carry`,
 * which keeps what still applies, or is dropped without one. The tab and the place in the
 * diff come with it. Nothing is written until the reader changes something, so a review only
 * opened stays out, and nothing at all while `off`, as once the review is decided.
 */
export function useReviewDraft<
  TDraft extends object,
  TTab extends string = string,
>(
  id: string,
  initial: () => TDraft,
  {
    base,
    carry,
    tab: firstTab,
    off,
  }: {
    base: string
    carry?: (saved: TDraft, fresh: TDraft) => TDraft
    tab?: TTab
    off?: boolean
  }
) {
  const [saved] = useState(() => (off ? undefined : read<TDraft>(id)))
  const [draft, setDraft] = useState<TDraft>(() => {
    const fresh = initial()
    if (saved?.draft === undefined) return fresh
    if (saved.base === base) return saved.draft
    return carry ? carry(saved.draft, fresh) : fresh
  })
  const [tab, setTab] = useState<TTab | undefined>(
    () => (saved?.tab as TTab | undefined) ?? firstTab
  )
  const first = useRef({ draft, tab })
  const dropped = useRef(false)
  useEffect(() => {
    if (off || dropped.current) return
    if (draft === first.current.draft && tab === first.current.tab) return
    write<TDraft>(id, { base, draft, tab })
  }, [id, base, draft, tab, off])
  return {
    draft,
    setDraft,
    /** Changes some of the draft's fields. */
    change: (patch: Partial<TDraft>) => setDraft((d) => ({ ...d, ...patch })),
    tab,
    setTab,
    /** Where the reader was in the diff. */
    place: saved?.place,
    /** Saves where the reader is in the diff, unless `off`. */
    onPlace: off
      ? undefined
      : (place: DiffPlace) => {
          if (!dropped.current) write(id, { place })
        },
    /** Forgets the review's entry and saves nothing more, as once it is sent. */
    drop: () => {
      dropped.current = true
      dropReview(id)
    },
  }
}
