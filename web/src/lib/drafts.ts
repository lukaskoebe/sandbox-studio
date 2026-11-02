import type { Mention } from "@/lib/mentions"

/** What a composer holds but hasn't sent. */
export type Draft = { text: string; files: File[]; mentions: Mention[] }

const empty: Draft = { text: "", files: [], mentions: [] }

// The drafts of composers that aren't on screen, by key. Files stay in memory only; the text
// and mentions are also in sessionStorage, so a reload of the tab keeps them.
const drafts = new Map<string, Draft>()

const storageKey = (key: string) => `studio.draft.${key}`

/** The unsent draft of the composer under key, or an empty one. */
export function loadDraft(key: string | undefined): Draft {
  if (!key) return empty
  const kept = drafts.get(key)
  if (kept) return kept
  try {
    const raw = sessionStorage.getItem(storageKey(key))
    if (raw) {
      const d = JSON.parse(raw) as Partial<Draft>
      if (typeof d.text === "string")
        return {
          text: d.text,
          files: [],
          mentions: Array.isArray(d.mentions) ? d.mentions : [],
        }
    }
  } catch {
    // No storage, or something else's value: the draft starts empty.
  }
  return empty
}

/** Keeps d as the draft under key; an empty draft forgets it. */
export function saveDraft(key: string | undefined, d: Draft) {
  if (!key) return
  const blank = !d.text && d.files.length === 0
  if (blank) drafts.delete(key)
  else drafts.set(key, d)
  try {
    if (!d.text) sessionStorage.removeItem(storageKey(key))
    else
      sessionStorage.setItem(
        storageKey(key),
        JSON.stringify({ text: d.text, mentions: d.mentions })
      )
  } catch {
    // The in-memory draft still comes back when the user returns.
  }
}
