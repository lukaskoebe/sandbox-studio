import { useSyncExternalStore } from "react"

/** How a full-length diff lays out its lines: one column, or old and new side by side. */
export type DiffStyle = "unified" | "split"

const key = "studio.diff.style"
const listeners = new Set<() => void>()

function read(): DiffStyle {
  try {
    return localStorage.getItem(key) === "split" ? "split" : "unified"
  } catch {
    return "unified"
  }
}

export function setDiffStyle(style: DiffStyle) {
  try {
    if (style === "split") localStorage.setItem(key, style)
    else localStorage.removeItem(key)
  } catch {
    // Without storage, the choice lasts until the page reloads.
  }
  current = style
  for (const l of listeners) l()
}

let current: DiffStyle | undefined

function subscribe(l: () => void) {
  listeners.add(l)
  const onStorage = (e: StorageEvent) => {
    if (e.key !== key) return
    current = read()
    l()
  }
  window.addEventListener("storage", onStorage)
  return () => {
    listeners.delete(l)
    window.removeEventListener("storage", onStorage)
  }
}

/** The diff style this browser picked last, the same in every diff and tab. */
export function useDiffStyle(): DiffStyle {
  return useSyncExternalStore(subscribe, () => (current ??= read()))
}
