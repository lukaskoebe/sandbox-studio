import {
  type KeyboardEvent,
  type PointerEvent,
  type ReactNode,
  useEffect,
  useRef,
  useState,
  useSyncExternalStore,
} from "react"
import { XIcon } from "@phosphor-icons/react"
import { Button } from "@/components/ui/button"
import { cn } from "@/lib/utils"

const wideQuery = "(min-width: 1024px)"
const defaultWidth = 420
const minWidth = 320
/** The panel never takes more than this share of the page, so the content keeps room. */
const maxShare = 0.7

/** Whether panels sit beside the content (lg and up) instead of covering the screen. */
export function useWideScreen() {
  return useSyncExternalStore(
    (onChange) => {
      const mql = window.matchMedia(wideQuery)
      mql.addEventListener("change", onChange)
      return () => mql.removeEventListener("change", onChange)
    },
    () => window.matchMedia(wideQuery).matches
  )
}

/**
 * The side panel of one page: beside its content on wide screens, resizable from its left
 * edge, and the full screen on narrow ones. The width is remembered under storageKey, so
 * each page keeps its own.
 */
export function SidePanel({
  label,
  title,
  meta,
  actions,
  storageKey,
  onClose,
  children,
}: {
  /** Names the panel for assistive tech. */
  label: string
  title: ReactNode
  /** Shown after the title, such as a status badge. */
  meta?: ReactNode
  /** Header buttons before the close button. */
  actions?: ReactNode
  storageKey: string
  onClose: () => void
  children: ReactNode
}) {
  const wide = useWideScreen()
  const ref = useRef<HTMLElement>(null)
  const closeRef = useRef<HTMLButtonElement>(null)
  const [width, setWidth] = useState(() => {
    const stored = Number(localStorage.getItem(storageKey))
    return Number.isFinite(stored) && stored >= minWidth ? stored : defaultWidth
  })
  useEffect(() => {
    localStorage.setItem(storageKey, String(width))
  }, [storageKey, width])
  // Full screen, the panel replaces the page, so focus moves into it.
  useEffect(() => {
    if (!wide) closeRef.current?.focus()
  }, [wide])

  // Widths are measured from the page the panel sits in, which is its parent.
  const clamp = (px: number) => {
    const page = ref.current?.parentElement?.getBoundingClientRect().width ?? 0
    return Math.round(
      Math.min(Math.max(minWidth, page * maxShare), Math.max(minWidth, px))
    )
  }
  const resizeTo = (clientX: number) => {
    const rect = ref.current?.parentElement?.getBoundingClientRect()
    if (rect) setWidth(clamp(rect.right - clientX))
  }
  const onResizeKey = (event: KeyboardEvent) => {
    if (event.key === "ArrowLeft") setWidth((w) => clamp(w + 16))
    else if (event.key === "ArrowRight") setWidth((w) => clamp(w - 16))
    else if (event.key === "Home") setWidth(clamp(Number.MAX_SAFE_INTEGER))
    else if (event.key === "End") setWidth(minWidth)
    else return
    event.preventDefault()
  }
  const onResizeDown = (event: PointerEvent<HTMLDivElement>) => {
    event.preventDefault()
    event.currentTarget.setPointerCapture(event.pointerId)
    resizeTo(event.clientX)
  }

  return (
    <aside
      ref={ref}
      aria-label={label}
      className={cn(
        "flex min-h-0 min-w-0 flex-col bg-background",
        wide ? "relative shrink-0 border-l" : "fixed inset-0 z-30"
      )}
      style={wide ? { width } : undefined}
      onKeyDown={(event) => {
        if (!wide && event.key === "Escape") onClose()
      }}
    >
      {wide && (
        <div
          role="separator"
          aria-label={`Resize ${label.toLowerCase()}`}
          aria-orientation="vertical"
          aria-valuemin={minWidth}
          aria-valuenow={width}
          tabIndex={0}
          className="absolute inset-y-0 -left-0.5 z-10 flex w-1 cursor-col-resize touch-none items-center justify-center outline-none hover:bg-primary/40 focus-visible:bg-primary/60"
          onPointerDown={onResizeDown}
          onPointerMove={(event) => {
            if (event.buttons === 1) resizeTo(event.clientX)
          }}
          onKeyDown={onResizeKey}
        >
          <span className="h-8 w-0.5 rounded-full bg-foreground/25" />
        </div>
      )}
      <header className="flex h-12 shrink-0 items-center gap-2 border-b px-3">
        <h2 className="min-w-0 truncate text-sm font-medium">{title}</h2>
        {meta}
        <div className="ml-auto flex shrink-0 items-center gap-1">
          {actions}
          <Button
            ref={closeRef}
            variant="ghost"
            size="icon-sm"
            aria-label="Close panel"
            title="Close panel"
            onClick={onClose}
          >
            <XIcon />
          </Button>
        </div>
      </header>
      <div className="flex min-h-0 min-w-0 flex-1 flex-col">{children}</div>
    </aside>
  )
}
