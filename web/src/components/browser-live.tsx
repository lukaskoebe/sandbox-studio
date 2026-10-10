import { useEffect, useRef, useState } from "react"
import { cn } from "@/lib/utils"

/**
 * The live view of a persona's browser: agent-browser's screencast, relayed by Studio over
 * a WebSocket, drawn on a canvas. Each frame is {"type":"frame","data":<base64 JPEG>,
 * "metadata":{deviceWidth,deviceHeight,...}}. While `driving`, mouse and keyboard events go
 * back as input_mouse and input_keyboard messages; Studio drops them otherwise.
 */
export function BrowserLive({
  url,
  driving,
  className,
}: {
  url: string
  driving: boolean
  className?: string
}) {
  const canvas = useRef<HTMLCanvasElement>(null)
  const socket = useRef<WebSocket | null>(null)
  const device = useRef({ width: 1280, height: 720 })
  const [state, setState] = useState<"connecting" | "live" | "closed">(
    "connecting"
  )
  const [notice, setNotice] = useState("")

  useEffect(() => {
    let stopped = false
    let retry: ReturnType<typeof setTimeout> | undefined
    const connect = () => {
      const ws = new WebSocket(url.replace(/^http/, "ws"))
      socket.current = ws
      setState("connecting")
      ws.onopen = () => {
        ws.send(JSON.stringify({ type: "config", maxFps: 10 }))
      }
      ws.onmessage = (ev) => {
        if (typeof ev.data !== "string") return
        let msg: {
          type?: string
          data?: string
          seq?: number
          metadata?: { deviceWidth?: number; deviceHeight?: number }
        }
        try {
          msg = JSON.parse(ev.data)
        } catch {
          return
        }
        if (msg.type !== "frame" || !msg.data) return
        if (typeof msg.seq === "number")
          ws.send(JSON.stringify({ type: "ack", seq: msg.seq }))
        // verify (live): the metadata names the page's size in CSS pixels.
        const w = msg.metadata?.deviceWidth
        const h = msg.metadata?.deviceHeight
        if (w && h) device.current = { width: w, height: h }
        const img = new Image()
        img.onload = () => {
          const c = canvas.current
          if (!c) return
          if (c.width !== img.width || c.height !== img.height) {
            c.width = img.width
            c.height = img.height
          }
          c.getContext("2d")?.drawImage(img, 0, 0)
        }
        img.src = `data:image/jpeg;base64,${msg.data}`
        setState("live")
      }
      ws.onclose = (ev) => {
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
      socket.current?.close()
    }
  }, [url])

  const send = (msg: object) => {
    const ws = socket.current
    if (driving && ws?.readyState === WebSocket.OPEN)
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
    if (!driving) return
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
        tabIndex={driving ? 0 : -1}
        aria-label="Live view of the browser"
        className={cn(
          "block h-auto w-full outline-none",
          driving
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
        onContextMenu={(e) => driving && e.preventDefault()}
        onKeyDown={(e) => key("keyDown", e)}
        onKeyUp={(e) => key("keyUp", e)}
      />
      {state !== "live" && (
        <p className="absolute inset-0 flex items-center justify-center p-4 text-center text-sm text-muted-foreground">
          {state === "connecting" ? "Connecting…" : notice}
        </p>
      )}
    </div>
  )
}
