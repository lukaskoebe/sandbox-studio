import { useEffect, useRef, useState } from "react"
import { cn } from "@/lib/utils"

/**
 * The live view of a teammate's browser: agent-browser's screencast, relayed by Studio over
 * a WebSocket, drawn on a canvas. Each frame is {"type":"frame","data":<base64 JPEG>,
 * "metadata":{deviceWidth,deviceHeight,...}}. While `driving`, mouse and keyboard events go
 * back as input_mouse and input_keyboard messages; Studio drops them otherwise.
 */
function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value)
}

export function BrowserLive({
  url,
  driving,
  inputEnabled = true,
  pageURL,
  onPageURL,
  className,
}: {
  url: string
  driving: boolean
  inputEnabled?: boolean
  pageURL?: string
  onPageURL?: (url: string) => void
  className?: string
}) {
  const canvas = useRef<HTMLCanvasElement>(null)
  const socket = useRef<WebSocket | null>(null)
  const device = useRef({ width: 1280, height: 720 })
  const onPageURLRef = useRef(onPageURL)
  const activePageURLRef = useRef(pageURL)
  const [state, setState] = useState<"connecting" | "live" | "closed">(
    "connecting"
  )
  const [notice, setNotice] = useState("")
  const [activePageURL, setActivePageURL] = useState(pageURL)
  const [renderedPageURL, setRenderedPageURL] = useState<string>()
  const [hasRenderedFrame, setHasRenderedFrame] = useState(false)

  useEffect(() => {
    onPageURLRef.current = onPageURL
  }, [onPageURL])

  useEffect(() => {
    if (pageURL === activePageURLRef.current) return
    activePageURLRef.current = pageURL
    setActivePageURL(pageURL)
    setRenderedPageURL(undefined)
  }, [pageURL])

  useEffect(() => {
    let stopped = false
    let retry: ReturnType<typeof setTimeout> | undefined
    let activeSocket: WebSocket | null = null
    const updatePageURL = (value: unknown) => {
      if (
        typeof value !== "string" ||
        value.length === 0 ||
        value === activePageURLRef.current
      )
        return
      activePageURLRef.current = value
      setActivePageURL(value)
      setRenderedPageURL(undefined)
      onPageURLRef.current?.(value)
    }
    const connect = () => {
      if (stopped) return
      let frameSerial = 0
      let paintedFrameSerial = 0
      const ws = new WebSocket(url.replace(/^http/, "ws"))
      activeSocket = ws
      socket.current = ws
      setState("connecting")
      setNotice("")
      setRenderedPageURL(undefined)
      setHasRenderedFrame(false)
      const isCurrent = () =>
        !stopped && activeSocket === ws && socket.current === ws
      ws.onopen = () => {
        if (!isCurrent()) return
        ws.send(JSON.stringify({ type: "config", maxFps: 10 }))
      }
      ws.onmessage = (ev) => {
        if (!isCurrent()) return
        if (typeof ev.data !== "string") return
        let parsed: unknown
        try {
          parsed = JSON.parse(ev.data)
        } catch {
          return
        }
        if (!isRecord(parsed) || typeof parsed.type !== "string") return
        const type = parsed.type
        if (type === "url") {
          updatePageURL(parsed.url)
          return
        }
        if (type === "tabs") {
          const tabs = parsed.tabs
          if (!Array.isArray(tabs)) return
          const activeTab = tabs.find(
            (tab) => isRecord(tab) && tab.active === true
          )
          if (isRecord(activeTab)) updatePageURL(activeTab.url)
          return
        }
        const data = parsed.data
        if (type !== "frame" || typeof data !== "string" || !data) return
        const frameNumber = ++frameSerial
        const framePageURL = activePageURLRef.current
        const seq = parsed.seq
        if (typeof seq === "number")
          ws.send(JSON.stringify({ type: "ack", seq }))
        // verify (live): the metadata names the page's size in CSS pixels.
        const metadataValue = parsed.metadata
        const metadata = isRecord(metadataValue) ? metadataValue : undefined
        const w = metadata?.deviceWidth
        const h = metadata?.deviceHeight
        if (
          typeof w === "number" &&
          Number.isFinite(w) &&
          w > 0 &&
          typeof h === "number" &&
          Number.isFinite(h) &&
          h > 0
        )
          device.current = { width: w, height: h }
        const img = new Image()
        img.onload = () => {
          if (!isCurrent() || frameNumber < paintedFrameSerial) return
          const c = canvas.current
          if (!c) return
          if (c.width !== img.width || c.height !== img.height) {
            c.width = img.width
            c.height = img.height
          }
          const context = c.getContext("2d")
          if (!context) return
          context.drawImage(img, 0, 0)
          paintedFrameSerial = frameNumber
          setRenderedPageURL(framePageURL)
          setHasRenderedFrame(true)
          setState("live")
        }
        img.src = `data:image/jpeg;base64,${data}`
      }
      ws.onclose = (ev) => {
        if (!isCurrent()) return
        activeSocket = null
        socket.current = null
        setState("closed")
        setNotice(ev.reason || "The live view disconnected")
        if (!stopped) retry = setTimeout(connect, 3000)
      }
    }
    connect()
    return () => {
      stopped = true
      clearTimeout(retry)
      const ws = activeSocket
      activeSocket = null
      if (socket.current === ws) socket.current = null
      ws?.close()
    }
  }, [url])

  const canDrive = driving && inputEnabled
  const send = (msg: object) => {
    const ws = socket.current
    if (canDrive && ws?.readyState === WebSocket.OPEN)
      ws.send(JSON.stringify(msg))
  }
  // CDP modifier bits: Alt 1, Control 2, Meta 4, Shift 8.
  const modifiers = (e: React.MouseEvent | React.KeyboardEvent) =>
    (e.altKey ? 1 : 0) |
    (e.ctrlKey ? 2 : 0) |
    (e.metaKey ? 4 : 0) |
    (e.shiftKey ? 8 : 0)
  const point = (e: React.MouseEvent) => {
    const r = e.currentTarget.getBoundingClientRect()
    return {
      x: Math.round(((e.clientX - r.left) / r.width) * device.current.width),
      y: Math.round(((e.clientY - r.top) / r.height) * device.current.height),
    }
  }
  const buttons = ["left", "middle", "right", "back", "forward"]
  const mouse = (eventType: string, e: React.MouseEvent, extra = {}) =>
    send({
      type: "input_mouse",
      eventType,
      ...point(e),
      button: eventType === "mouseMoved" ? "none" : buttons[e.button],
      clickCount: eventType === "mouseMoved" ? 0 : Math.max(1, e.detail),
      modifiers: modifiers(e),
      ...extra,
    })
  const lastMove = useRef(0)
  const key = (eventType: string, e: React.KeyboardEvent) => {
    if (!canDrive) return
    e.preventDefault()
    send({
      type: "input_keyboard",
      eventType,
      key: e.key,
      code: e.code,
      text: eventType === "keyDown" && e.key.length === 1 ? e.key : undefined,
      windowsVirtualKeyCode: e.keyCode,
      modifiers: modifiers(e),
    })
  }

  return (
    <div className={cn("relative bg-muted", className)}>
      <canvas
        ref={canvas}
        tabIndex={canDrive ? 0 : -1}
        aria-label="Live view of the browser"
        className={cn(
          "block h-auto w-full outline-none",
          canDrive
            ? "cursor-default ring-2 ring-primary"
            : "pointer-events-none",
          state !== "live" && "opacity-40"
        )}
        onMouseDown={(e) => {
          e.currentTarget.focus()
          mouse("mousePressed", e)
        }}
        onMouseUp={(e) => mouse("mouseReleased", e)}
        onMouseMove={(e) => {
          const now = performance.now()
          if (now - lastMove.current < 50) return
          lastMove.current = now
          mouse("mouseMoved", e)
        }}
        onWheel={(e) =>
          mouse("mouseWheel", e, { deltaX: e.deltaX, deltaY: e.deltaY })
        }
        onContextMenu={(e) => canDrive && e.preventDefault()}
        onKeyDown={(e) => key("keyDown", e)}
        onKeyUp={(e) => key("keyUp", e)}
      />
      {state !== "live" && (
        <p className="absolute inset-0 flex items-center justify-center p-4 text-center text-sm text-muted-foreground">
          {state === "connecting" ? "Connecting…" : notice}
        </p>
      )}
      {state === "live" &&
        hasRenderedFrame &&
        activePageURL === "about:blank" &&
        renderedPageURL === "about:blank" && (
          <div className="pointer-events-none absolute inset-0 flex items-center justify-center bg-background/85 p-4 text-center">
            <div className="max-w-sm space-y-1">
              <p className="text-sm font-medium">
                This browser is on a blank page.
              </p>
              <p className="text-sm text-muted-foreground">
                {driving
                  ? "Enter a website in the address bar above."
                  : "Take over, then enter a website in the address bar above."}
              </p>
            </div>
          </div>
        )}
    </div>
  )
}
