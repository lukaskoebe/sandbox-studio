import {
  type KeyboardEvent,
  type PointerEvent,
  useCallback,
  useEffect,
  useRef,
  useState,
} from "react"
import { cn } from "@/lib/utils"

const resetEvent = "studio:reset-sizes"

/** Puts every split back to its default size, and unfolds what folded. */
export function resetStoredSizes() {
  window.dispatchEvent(new Event(resetEvent))
}

/** A size kept in this browser under key, so each split keeps its own. */
export function useStoredSize(key: string, fallback: number) {
  const [size, setSize] = useState(() => {
    try {
      const stored = Number(localStorage.getItem(key))
      return stored > 0 && Number.isFinite(stored) ? stored : fallback
    } catch {
      return fallback
    }
  })
  useEffect(() => {
    const reset = () => setSize(fallback)
    window.addEventListener(resetEvent, reset)
    return () => window.removeEventListener(resetEvent, reset)
  }, [fallback])
  useEffect(() => {
    try {
      localStorage.setItem(key, String(size))
    } catch {
      // Private windows and blocked storage only lose the size.
    }
  }, [key, size])
  return [size, setSize] as const
}

/** A flag kept in this browser under key, as useStoredSize keeps a size. */
export function useStoredFlag(key: string, fallback: boolean) {
  const [on, setOn] = useState(() => {
    try {
      const stored = localStorage.getItem(key)
      return stored === null ? fallback : stored === "1"
    } catch {
      return fallback
    }
  })
  useEffect(() => {
    const reset = () => setOn(fallback)
    window.addEventListener(resetEvent, reset)
    return () => window.removeEventListener(resetEvent, reset)
  }, [fallback])
  useEffect(() => {
    try {
      localStorage.setItem(key, on ? "1" : "0")
    } catch {
      // As above.
    }
  }, [key, on])
  return [on, setOn] as const
}

/** The width an element has now, kept up to date as it changes. */
export function useElementWidth<TElement extends HTMLElement>() {
  return useElementSize<TElement>("width")
}

/** The height an element has now, as useElementWidth keeps its width. */
export function useElementHeight<TElement extends HTMLElement>() {
  return useElementSize<TElement>("height")
}

function useElementSize<TElement extends HTMLElement>(of: "width" | "height") {
  const [el, setEl] = useState<TElement | null>(null)
  const [size, setSize] = useState(0)
  useEffect(() => {
    if (!el) return
    const observer = new ResizeObserver(([entry]) =>
      setSize(Math.round(entry.contentRect[of]))
    )
    observer.observe(el)
    return () => observer.disconnect()
  }, [el, of])
  return [setEl, size] as const
}

/**
 * The list beside what it opens, as in Changes and Files: one width for both, kept in this
 * browser, and what it leaves the content beside it.
 */
export const railSplit = {
  key: "studio.rail-width",
  default: 272,
  min: 200,
  max: 480,
  contentMin: 240,
}

/** How far past its minimum a pane is dragged before it folds. */
const foldSlack = 48
/** How far an arrow key moves the line. */
const step = 16

/**
 * The line between two panes, which drags: the pane on `side` of it takes the size it is dragged
 * to, between min and max. Dragged well past the minimum it folds, if it can, and dragged out a
 * little it opens at its minimum. Where the other pane folds too, dragged well past the maximum
 * it expands, leaving the other pane folded, and dragged back a little it gives that pane its
 * minimum.
 * Double-click puts back the default; with focus, ← → (↑ ↓ for a pane above or below) move it,
 * Home and End go to the ends and Enter folds or unfolds.
 */
export function ResizeHandle({
  label,
  side,
  size,
  min,
  max,
  defaultSize,
  onResize,
  folded = false,
  foldedSize = 0,
  onFold,
  expanded = false,
  expandedSize = 0,
  onExpand,
  className,
}: {
  /** Names what it resizes, such as "the sidebar". */
  label: string
  /** Which side of the line the pane being sized is on: above or below for a line across. */
  side: "left" | "right" | "top" | "bottom"
  size: number
  min: number
  max: number
  defaultSize: number
  onResize: (size: number) => void
  folded?: boolean
  /** What is left of the pane folded, such as the sidebar's icons. */
  foldedSize?: number
  /** Without it the pane doesn't fold. */
  onFold?: (folded: boolean) => void
  /** The pane has all but the other pane's folded part. */
  expanded?: boolean
  /** The pane's size while it is expanded. */
  expandedSize?: number
  /** Without it the pane doesn't expand: it stops at max. */
  onExpand?: (expanded: boolean) => void
  className?: string
}) {
  const start = useRef<{ x: number; size: number } | null>(null)
  // A line across, between a pane above and one below, drags up and down.
  const across = side === "top" || side === "bottom"
  const grows = side === "left" || side === "top"
  const [dragging, setDragging] = useState(false)
  const clamp = useCallback(
    (px: number) => Math.round(Math.min(Math.max(px, min), Math.max(min, max))),
    [min, max]
  )
  // While dragging, nothing under the pointer is selected and nothing animates its width.
  useEffect(() => {
    if (!dragging) return
    document.body.dataset.resizing = across ? "row" : ""
    return () => {
      delete document.body.dataset.resizing
    }
  }, [dragging, across])

  const moveTo = (clientX: number) => {
    const from = start.current
    if (!from) return
    const dx = clientX - from.x
    const wanted = from.size + (grows ? dx : -dx)
    // A folded or expanded pane opens to the near end of its range as soon as it is dragged a
    // little, and follows the pointer from there; dragged back as far, it folds or expands again.
    if (folded && onFold) {
      if (wanted < foldedSize + foldSlack) return
      onFold(false)
      start.current = { x: clientX, size: min }
      onResize(clamp(min))
      return
    }
    if (expanded && onExpand) {
      if (wanted > expandedSize - foldSlack) return
      onExpand(false)
      start.current = { x: clientX, size: max }
      onResize(clamp(max))
      return
    }
    if (onFold && wanted < min - foldSlack) {
      onFold(true)
      start.current = { x: clientX, size: foldedSize }
      return
    }
    if (onExpand && wanted > max + foldSlack) {
      onExpand(true)
      start.current = { x: clientX, size: expandedSize }
      return
    }
    onResize(clamp(wanted))
  }
  const onPointerDown = (event: PointerEvent<HTMLDivElement>) => {
    if (event.button !== 0) return
    event.preventDefault()
    event.currentTarget.setPointerCapture(event.pointerId)
    // A folded pane is dragged out from what is left of it.
    start.current = {
      x: across ? event.clientY : event.clientX,
      size: folded ? foldedSize : expanded ? expandedSize : size,
    }
    setDragging(true)
  }
  const onPointerUp = () => {
    start.current = null
    setDragging(false)
  }
  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    const [forward, back] = across
      ? ["ArrowDown", "ArrowUp"]
      : ["ArrowRight", "ArrowLeft"]
    const grow = grows ? forward : back
    const shrink = grows ? back : forward
    if (event.key === "Enter" && onFold) onFold(!folded)
    else if (folded && (event.key === grow || event.key === "End")) {
      onFold?.(false)
    } else if (expanded && (event.key === shrink || event.key === "Home")) {
      onExpand?.(false)
      onResize(clamp(max))
    } else if (event.key === grow) {
      if (onExpand && size >= max) onExpand(true)
      else onResize(clamp(size + step))
    } else if (event.key === shrink) {
      if (onFold && size <= min) onFold(true)
      else onResize(clamp(size - step))
    } else if (event.key === "Home") onResize(min)
    else if (event.key === "End") onResize(clamp(max))
    else return
    event.preventDefault()
  }

  return (
    <div
      role="separator"
      aria-label={`Resize ${label}`}
      aria-orientation={across ? "horizontal" : "vertical"}
      aria-valuemin={min}
      aria-valuemax={Math.round(max)}
      aria-valuenow={folded ? 0 : expanded ? Math.round(max) : size}
      tabIndex={0}
      title="Drag to resize, double-click to reset"
      data-dragging={dragging || undefined}
      className={cn(
        "group/handle z-20 shrink-0 touch-none outline-none",
        across ? "h-2 cursor-row-resize" : "w-2 cursor-col-resize",
        className
      )}
      onPointerDown={onPointerDown}
      onPointerMove={(event) => {
        if (start.current) moveTo(across ? event.clientY : event.clientX)
      }}
      onPointerUp={onPointerUp}
      onPointerCancel={onPointerUp}
      onLostPointerCapture={onPointerUp}
      onDoubleClick={() => {
        if (folded) onFold?.(false)
        else if (expanded) onExpand?.(false)
        onResize(clamp(defaultSize))
      }}
      onKeyDown={onKeyDown}
    >
      <span
        className={cn(
          "pointer-events-none block transition-colors group-hover/handle:bg-primary/60 group-focus-visible/handle:bg-primary group-data-dragging/handle:bg-primary",
          across ? "my-auto h-0.5 w-full" : "mx-auto h-full w-0.5"
        )}
      />
    </div>
  )
}
