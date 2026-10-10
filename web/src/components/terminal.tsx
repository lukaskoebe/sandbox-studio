import { useEffect, useRef } from "react"
import { Terminal as XTerm } from "@xterm/xterm"
import { FitAddon } from "@xterm/addon-fit"
import { WebLinksAddon } from "@xterm/addon-web-links"
import { ClipboardAddon } from "@xterm/addon-clipboard"
import { WebglAddon } from "@xterm/addon-webgl"
import "@xterm/xterm/css/xterm.css"
import { cn } from "@/lib/utils"

const theme = {
  background: "#1c1917",
  foreground: "#e7e5e4",
  cursor: "#e7e5e4",
  selectionBackground: "#57534e",
  black: "#292524",
  brightBlack: "#78716c",
}

/**
 * One tmux session in the guest, attached over a websocket. Binary messages carry terminal
 * bytes; text messages are control messages. The connection is retried until the session
 * ends, so a Studio restart or a sandbox reboot only shows a short notice. While the sandbox
 * is suspended the terminal parks instead of retrying, and reattaches on resume.
 */
export function Terminal({
  url,
  active,
  suspended = false,
  onExit,
  className,
}: {
  url: string
  active: boolean
  suspended?: boolean
  onExit: () => void
  className?: string
}) {
  const container = useRef<HTMLDivElement>(null)
  const fitRef = useRef<FitAddon>(null)
  const termRef = useRef<XTerm>(null)
  const onExitRef = useRef(onExit)
  onExitRef.current = onExit
  const suspendedRef = useRef(suspended)
  const parkRef = useRef<(suspended: boolean) => void>(null)

  useEffect(() => {
    const el = container.current
    if (!el) return
    const term = new XTerm({
      fontFamily:
        '"JetBrains Mono", ui-monospace, SFMono-Regular, Menlo, monospace',
      fontSize: 13,
      cursorBlink: true,
      theme,
      allowProposedApi: true,
      macOptionClickForcesSelection: true,
    })
    const fit = new FitAddon()
    term.loadAddon(fit)
    term.loadAddon(new WebLinksAddon())
    term.loadAddon(new ClipboardAddon())
    term.open(el)
    try {
      term.loadAddon(new WebglAddon())
    } catch {
      // The DOM renderer is fine where WebGL is unavailable.
    }
    termRef.current = term
    fitRef.current = fit

    const encoder = new TextEncoder()
    let ws: WebSocket | null = null
    let disposed = false
    let retry: ReturnType<typeof setTimeout> | undefined
    let attempts = 0
    let parked = false

    const park = () => {
      if (parked) return
      parked = true
      clearTimeout(retry)
      term.write("\r\n\x1b[2m[Sandbox suspended]\x1b[0m\r\n")
    }

    const send = (data: string | Uint8Array<ArrayBuffer>) => {
      if (ws?.readyState === WebSocket.OPEN) ws.send(data)
    }
    const connect = () => {
      if (suspendedRef.current) return park()
      const proto = window.location.protocol === "https:" ? "wss:" : "ws:"
      const sock = new WebSocket(
        `${proto}//${window.location.host}${url}?cols=${term.cols}&rows=${term.rows}`
      )
      ws = sock
      sock.binaryType = "arraybuffer"
      sock.onopen = () => {
        attempts = 0
      }
      sock.onmessage = (e) => term.write(new Uint8Array(e.data as ArrayBuffer))
      sock.onclose = (e) => {
        // A socket closed by park() may report after resume opened its successor.
        if (disposed || ws !== sock) return
        if (e.code === 1000 && e.reason === "exited") {
          onExitRef.current()
          return
        }
        if (suspendedRef.current) return park()
        if (attempts === 0)
          term.write("\r\n\x1b[2m[connection lost, reconnecting…]\x1b[0m\r\n")
        attempts++
        retry = setTimeout(connect, Math.min(500 * attempts, 5000))
      }
    }

    const subs = [
      term.onData((d) => send(encoder.encode(d))),
      term.onBinary((d) => send(Uint8Array.from(d, (c) => c.charCodeAt(0)))),
      term.onResize(({ cols, rows }) =>
        send(JSON.stringify({ type: "resize", cols, rows }))
      ),
    ]
    // Attach once the terminal is visible and sized: the shell starts at the size we send,
    // and tabs that are never shown never connect.
    let started = false
    const observer = new ResizeObserver(() => {
      if (el.offsetWidth === 0 || el.offsetHeight === 0) return
      fit.fit()
      if (!started) {
        started = true
        connect()
      }
    })
    observer.observe(el)
    parkRef.current = (on) => {
      if (on) {
        if (!started) return
        park()
        ws?.close()
      } else if (parked) {
        parked = false
        attempts = 0
        connect()
      }
    }

    return () => {
      parkRef.current = null
      disposed = true
      clearTimeout(retry)
      observer.disconnect()
      subs.forEach((s) => s.dispose())
      ws?.close()
      term.dispose()
    }
  }, [url])

  useEffect(() => {
    suspendedRef.current = suspended
    parkRef.current?.(suspended)
  }, [suspended])

  useEffect(() => {
    if (!active) return
    fitRef.current?.fit()
    termRef.current?.focus()
  }, [active])

  return (
    <div
      className={cn(
        "size-full overflow-hidden bg-[#1c1917] p-2",
        !active && "hidden",
        className
      )}
    >
      <div ref={container} className="size-full" />
    </div>
  )
}
