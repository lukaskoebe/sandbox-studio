import { useCallback, useEffect, useRef, useState } from "react"

/** A point of the page, in CSS pixels of its viewport. */
export type PagePoint = { x: number; y: number }

/**
 * Asks the page for the cursor it shows at a point: a CSS cursor keyword, or undefined when
 * the page didn't say this time, as when it was busy. It throws when the lookup failed.
 */
export type CursorLookup = (at: PagePoint) => Promise<string | undefined>

// The cursors Studio passes on, as the host does (internal/browser/element.go).
const pageCursors = new Set([
  "default",
  "context-menu",
  "help",
  "pointer",
  "progress",
  "wait",
  "cell",
  "crosshair",
  "text",
  "vertical-text",
  "alias",
  "copy",
  "move",
  "no-drop",
  "not-allowed",
  "grab",
  "grabbing",
  "all-scroll",
  "col-resize",
  "row-resize",
  "n-resize",
  "e-resize",
  "s-resize",
  "w-resize",
  "ne-resize",
  "nw-resize",
  "se-resize",
  "sw-resize",
  "ew-resize",
  "ns-resize",
  "nesw-resize",
  "nwse-resize",
  "zoom-in",
  "zoom-out",
])

/** At most one lookup this often while the mouse moves, and never two at once. */
const cursorEvery = 120
/** Lookups that fail this many times in a row stop, as on a guest from before them. */
const cursorFailures = 5

/**
 * Follows the cursor the page shows under the mouse, while the user drives: track() each point
 * the mouse moves to, and the view shows cursor. Lookups are throttled, the newest point wins,
 * and a page that can't answer keeps the cursor it had.
 */
export function usePageCursor(lookup: CursorLookup | undefined, on: boolean) {
  const [cursor, setCursor] = useState("default")
  const lookupRef = useRef(lookup)
  const onRef = useRef(on)
  useEffect(() => {
    lookupRef.current = lookup
    onRef.current = on
  })
  const state = useRef<{
    at?: PagePoint
    busy: boolean
    last: number
    failed: number
    timer?: ReturnType<typeof setTimeout>
  }>({ busy: false, last: 0, failed: 0 })
  const ask = useRef(() => {})
  useEffect(() => {
    const s = state.current
    ask.current = () => {
      const find = lookupRef.current
      if (
        s.busy ||
        s.failed >= cursorFailures ||
        !s.at ||
        !find ||
        !onRef.current
      )
        return
      const wait = s.last + cursorEvery - performance.now()
      if (wait > 0) {
        s.timer ??= setTimeout(() => {
          s.timer = undefined
          ask.current()
        }, wait)
        return
      }
      const at = s.at
      s.at = undefined
      s.busy = true
      s.last = performance.now()
      find(at)
        .then(
          (c) => {
            s.failed = 0
            if (c && pageCursors.has(c) && onRef.current) setCursor(c)
          },
          () => {
            s.failed++
          }
        )
        .finally(() => {
          s.busy = false
          ask.current()
        })
    }
    return () => {
      clearTimeout(s.timer)
      s.timer = undefined
      ask.current = () => {}
    }
  }, [])
  const track = useCallback((at: PagePoint) => {
    state.current.at = at
    ask.current()
  }, [])
  return { cursor: on ? cursor : undefined, track }
}
