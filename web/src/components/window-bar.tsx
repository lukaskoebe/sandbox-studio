import { useEffect, useRef, useState, type MouseEvent } from "react"
import { CopyIcon, MinusIcon, SquareIcon, XIcon } from "@phosphor-icons/react"
import {
  appWindow,
  windowAction,
  windowDrag,
  windowResize,
  type WindowAction,
  type WindowEdge,
} from "@/lib/desktop"
import { cn } from "@/lib/utils"

/** Whether the window fills the screen's work area, as a maximised one does. */
function useMaximised() {
  const [maximised, setMaximised] = useState(false)
  useEffect(() => {
    const check = () =>
      setMaximised(
        window.outerWidth >= window.screen.availWidth &&
          window.outerHeight >= window.screen.availHeight
      )
    check()
    window.addEventListener("resize", check)
    return () => window.removeEventListener("resize", check)
  }, [])
  return maximised
}

function WindowButton({
  action,
  label,
  className,
  children,
}: {
  action: WindowAction
  label: string
  className?: string
  children: React.ReactNode
}) {
  return (
    <button
      type="button"
      tabIndex={-1}
      aria-label={label}
      title={label}
      className={cn(
        "flex h-full w-9 items-center justify-center text-muted-foreground transition-colors hover:bg-muted hover:text-foreground",
        className
      )}
      // The focus stays where it is, as with a window manager's buttons.
      onMouseDown={(e) => e.preventDefault()}
      onDoubleClick={(e) => e.stopPropagation()}
      onClick={() => windowAction(action)}
    >
      {children}
    </button>
  )
}

/**
 * The top of the app's main column, in place of the system's title bar: it moves the window,
 * and on Linux, where the window has no frame, has its buttons. macOS moves the window itself
 * and draws its own buttons, above the sidebar. In a browser it isn't there (docs/app.md).
 */
export function WindowBar() {
  const armed = useRef(false)
  const maximised = useMaximised()
  if (!appWindow) return null
  if (appWindow !== "frameless")
    return (
      <div
        data-testid="window-bar"
        className="h-7 shrink-0"
        onDoubleClick={() => windowAction("maximise")}
      />
    )

  const start = () => {
    if (armed.current) windowDrag()
    armed.current = false
  }
  return (
    <div
      data-testid="window-bar"
      className="flex h-7 shrink-0 justify-end bg-background select-none"
      // A press only arms the move: a double click maximises, and the press keeps the focus
      // where it is, in a terminal say.
      onMouseDown={(e: MouseEvent) => {
        e.preventDefault()
        armed.current = e.button === 0 && e.detail === 1
      }}
      onMouseMove={(e: MouseEvent) => {
        if (e.buttons & 1) start()
        else armed.current = false
      }}
      onMouseLeave={(e: MouseEvent) => {
        if (e.buttons & 1) start()
        else armed.current = false
      }}
      onMouseUp={() => (armed.current = false)}
      onDoubleClick={() => windowAction("maximise")}
    >
      <WindowButton action="minimise" label="Minimise">
        <MinusIcon className="size-3.5" />
      </WindowButton>
      <WindowButton
        action="maximise"
        label={maximised ? "Restore" : "Maximise"}
      >
        {maximised ? (
          <CopyIcon className="size-3.5" />
        ) : (
          <SquareIcon className="size-3" />
        )}
      </WindowButton>
      <WindowButton
        action="close"
        label="Close"
        className="hover:bg-destructive hover:text-white"
      >
        <XIcon className="size-3.5" />
      </WindowButton>
    </div>
  )
}

const edges: { edge: WindowEdge; className: string }[] = [
  { edge: "n", className: "inset-x-2.5 top-0 h-1 cursor-ns-resize" },
  { edge: "s", className: "inset-x-2.5 bottom-0 h-1 cursor-ns-resize" },
  { edge: "w", className: "inset-y-2.5 left-0 w-1 cursor-ew-resize" },
  { edge: "e", className: "inset-y-2.5 right-0 w-1 cursor-ew-resize" },
  { edge: "nw", className: "top-0 left-0 size-2.5 cursor-nwse-resize" },
  { edge: "ne", className: "top-0 right-0 size-1 cursor-nesw-resize" },
  { edge: "sw", className: "bottom-0 left-0 size-2.5 cursor-nesw-resize" },
  { edge: "se", className: "right-0 bottom-0 size-2.5 cursor-nwse-resize" },
]

/**
 * The thin strips along a frameless window's sides that resize it. The window's frame would
 * have these; a maximised window has nothing to resize.
 */
export function WindowEdges() {
  const maximised = useMaximised()
  if (appWindow !== "frameless" || maximised) return null
  return edges.map(({ edge, className }) => (
    <div
      key={edge}
      data-testid={`window-edge-${edge}`}
      className={cn("fixed z-[60]", className)}
      onMouseDown={(e) => {
        if (e.button !== 0) return
        e.preventDefault()
        windowResize(edge)
      }}
    />
  ))
}
