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
  const [el, setEl] = useState<TElement | null>(null)
  const [width, setWidth] = useState(0)
  useEffect(() => {
    if (!el) return
    const observer = new ResizeObserver(([entry]) =>
      setWidth(Math.round(entry.contentRect.width))
    )
    observer.observe(el)
    return () => observer.disconnect()
  }, [el])
  return [setEl, width] as const
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
 * to, between min and max. Dragged well past the minimum it folds, if it can, and dragged out
 * again it unfolds. Double-click puts back the default; with focus, ← → move it, Home and End go
 * to the ends and Enter folds or unfolds.
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
  className,
}: {
  /** Names what it resizes, such as "the sidebar". */
  label: string
  /** Which side of the line the pane being sized is on. */
  side: "left" | "right"
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
  className?: string
}) {
  const start = useRef<{ x: number; size: number } | null>(null)
  const [dragging, setDragging] = useState(false)
  const clamp = useCallback(
    (px: number) => Math.round(Math.min(Math.max(px, min), Math.max(min, max))),
    [min, max]
  )
  // While dragging, nothing under the pointer is selected and nothing animates its width.
  useEffect(() => {
    if (!dragging) return
    document.body.dataset.resizing = ""
    return () => {
      delete document.body.dataset.resizing
    }
  }, [dragging])

  const moveTo = (clientX: number) => {
    if (!start.current) return
    const dx = clientX - start.current.x
    const wanted = start.current.size + (side === "left" ? dx : -dx)
    if (onFold && wanted < min - foldSlack) {
      if (!folded) onFold(true)
      return
    }
    if (folded && onFold) {
      if (wanted < min) return
      onFold(false)
    }
    onResize(clamp(wanted))
  }
  const onPointerDown = (event: PointerEvent<HTMLDivElement>) => {
    if (event.button !== 0) return
    event.preventDefault()
    event.currentTarget.setPointerCapture(event.pointerId)
    // A folded pane is dragged out from what is left of it.
    start.current = { x: event.clientX, size: folded ? foldedSize : size }
    setDragging(true)
  }
  const onPointerUp = () => {
    start.current = null
    setDragging(false)
  }
  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    const grow = side === "left" ? "ArrowRight" : "ArrowLeft"
    const shrink = side === "left" ? "ArrowLeft" : "ArrowRight"
    if (event.key === "Enter" && onFold) onFold(!folded)
    else if (folded && (event.key === grow || event.key === "End")) {
      onFold?.(false)
    } else if (event.key === grow) onResize(clamp(size + step))
    else if (event.key === shrink) {
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
      aria-orientation="vertical"
      aria-valuemin={min}
      aria-valuemax={Math.round(max)}
      aria-valuenow={folded ? 0 : size}
      tabIndex={0}
      title="Drag to resize, double-click to reset"
      data-dragging={dragging || undefined}
      className={cn(
        "group/handle z-20 w-2 shrink-0 cursor-col-resize touch-none outline-none",
        className
      )}
      onPointerDown={onPointerDown}
      onPointerMove={(event) => {
        if (start.current) moveTo(event.clientX)
      }}
      onPointerUp={onPointerUp}
      onPointerCancel={onPointerUp}
      onLostPointerCapture={onPointerUp}
      onDoubleClick={() => {
        onFold?.(false)
        onResize(clamp(defaultSize))
      }}
      onKeyDown={onKeyDown}
    >
      <span className="pointer-events-none mx-auto block h-full w-0.5 transition-colors group-hover/handle:bg-primary/60 group-focus-visible/handle:bg-primary group-data-dragging/handle:bg-primary" />
    </div>
  )
}
