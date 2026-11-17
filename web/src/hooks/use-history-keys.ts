import { useEffect } from "react"
import { useRouter } from "@tanstack/react-router"
import { isMac } from "@/lib/keys"

/** Where the keys belong to what has the focus: text moves by word there, and the terminal
 * sends them to its shell. The live view of a browser you drive takes them too, and says so
 * with preventDefault. */
const ownKeys =
  "input, textarea, select, [contenteditable]:not([contenteditable=false]), .xterm"

/**
 * Alt+← and Alt+→ go back and forward through Studio's pages, as in a browser, and ⌘[ and ⌘] on
 * a Mac, as in Safari. The desktop app's webview has no such keys of its own; a browser that
 * does gets the key handled once, here. Back stops at Studio's first page, where the key is the
 * browser's again: it leaves Studio there, and does nothing in the app.
 */
export function useHistoryKeys() {
  const router = useRouter()
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.defaultPrevented || e.repeat || e.isComposing) return
      const alt = e.altKey && !e.ctrlKey && !e.metaKey && !e.shiftKey
      const cmd = isMac && e.metaKey && !e.ctrlKey && !e.altKey && !e.shiftKey
      const to =
        (alt && e.key === "ArrowLeft") || (cmd && e.code === "BracketLeft")
          ? "back"
          : (alt && e.key === "ArrowRight") ||
              (cmd && e.code === "BracketRight")
            ? "forward"
            : undefined
      if (!to) return
      const t = e.target
      if (t instanceof Element && t.closest(ownKeys)) return
      if (to === "back") {
        if (!router.history.canGoBack()) return
        router.history.back()
      } else router.history.forward()
      e.preventDefault()
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [router])
}
