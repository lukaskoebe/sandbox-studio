/** Whether shortcuts take ⌘ rather than Ctrl here. */
export const isMac =
  typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.platform)

/** The modifier as shortcuts show it: "⌘" on a Mac, "Ctrl+" elsewhere, as in "⌘K" or "Ctrl+K". */
export const modKey = isMac ? "⌘" : "Ctrl+"

/** Whether a key event holds the shortcut modifier (⌘ on a Mac, Ctrl elsewhere) and only it. */
export function hasModKey(e: {
  metaKey: boolean
  ctrlKey: boolean
  altKey: boolean
  shiftKey: boolean
}) {
  return (
    (isMac ? e.metaKey && !e.ctrlKey : e.ctrlKey && !e.metaKey) &&
    !e.altKey &&
    !e.shiftKey
  )
}
