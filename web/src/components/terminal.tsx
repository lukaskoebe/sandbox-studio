import { useCallback, useEffect, useRef, useState } from "react"
import { Terminal as XTerm } from "@xterm/xterm"
import { FitAddon } from "@xterm/addon-fit"
import { WebLinksAddon } from "@xterm/addon-web-links"
import { ClipboardAddon, type IClipboardProvider } from "@xterm/addon-clipboard"
import { WebglAddon } from "@xterm/addon-webgl"
import "@xterm/xterm/css/xterm.css"
import { TerminalControls } from "@/components/terminal-controls"
import { copyText } from "@/lib/clipboard"
import { cn } from "@/lib/utils"

const theme = {
  background: "#1c1917",
  foreground: "#e7e5e4",
  cursor: "#e7e5e4",
  selectionBackground: "#57534e",
  black: "#292524",
  brightBlack: "#78716c",
}

const controlArrowKeys: Record<string, string> = {
  "\x1b[A": "\x1b[1;5A",
  "\x1b[B": "\x1b[1;5B",
  "\x1b[C": "\x1b[1;5C",
  "\x1b[D": "\x1b[1;5D",
  "\x1bOA": "\x1b[1;5A",
  "\x1bOB": "\x1b[1;5B",
  "\x1bOC": "\x1b[1;5C",
  "\x1bOD": "\x1b[1;5D",
}

const controlCharacters: Record<string, string> = {
  " ": "\x00",
  "@": "\x00",
  "2": "\x00",
  "3": "\x1b",
  "4": "\x1c",
  "5": "\x1d",
  "6": "\x1e",
  "7": "\x1f",
  "8": "\x7f",
  "[": "\x1b",
  "\\": "\x1c",
  "]": "\x1d",
  "^": "\x1e",
  _: "\x1f",
  "?": "\x7f",
}

function applyControlKey(data: string): string | null {
  if (Object.prototype.hasOwnProperty.call(controlArrowKeys, data)) {
    return controlArrowKeys[data]
  }

  const keys = Array.from(data)
  if (keys.length !== 1) return null

  const key = keys[0]
  const code = key.codePointAt(0)
  if (code === undefined) return null
  if (code <= 0x1f || code === 0x7f) return data

  if (Object.prototype.hasOwnProperty.call(controlCharacters, key)) {
    return controlCharacters[key]
  }
  if (/^[a-z]$/i.test(key)) return String.fromCharCode(code & 0x1f)

  // A single non-ASCII character is still one user key. It has no standard
  // terminal Ctrl representation, so pass it through and disarm Ctrl.
  return data
}

const terminalClipboard: IClipboardProvider = {
  async readText(selection) {
    if (String(selection) !== "c" || typeof navigator === "undefined") {
      return ""
    }
    try {
      const clipboard = (navigator as Partial<Navigator>).clipboard
      if (!clipboard || typeof clipboard.readText !== "function") return ""
      return await clipboard.readText()
    } catch {
      return ""
    }
  },
  writeText(selection, text) {
    if (String(selection) !== "c") return undefined
    // OSC 52 clipboard writes are best-effort terminal protocol requests. The
    // addon must not surface a denied browser clipboard write as an input error.
    return copyText(text).catch(() => undefined)
  },
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
  const [connected, setConnected] = useState(false)
  const [ctrlArmed, setCtrlArmed] = useState(false)
  const connectedRef = useRef(false)
  const ctrlArmedRef = useRef(false)
  const pasteEventPendingRef = useRef(false)
  const onExitRef = useRef(onExit)
  onExitRef.current = onExit
  const suspendedRef = useRef(suspended)
  const parkRef = useRef<(suspended: boolean) => void>(null)

  const consumeCtrl = useCallback((data: string) => {
    if (!ctrlArmedRef.current || pasteEventPendingRef.current) return data
    const controlledData = applyControlKey(data)
    if (controlledData === null) return data
    ctrlArmedRef.current = false
    setCtrlArmed(false)
    return controlledData
  }, [])

  const sendTerminalInput = useCallback((data: string) => {
    if (!connectedRef.current || suspendedRef.current) return
    const term = termRef.current
    if (!term) return
    const arrow =
      data.length === 3 && data.startsWith("\x1b[") && "ABCD".includes(data[2])
    // Match xterm's physical arrow-key output when the shell enabled DECCKM.
    term.input(
      arrow && term.modes.applicationCursorKeysMode ? `\x1bO${data[2]}` : data,
      true
    )
  }, [])

  const focusTerminal = useCallback(() => {
    if (!connectedRef.current || suspendedRef.current) return
    termRef.current?.focus()
  }, [])

  const toggleCtrl = useCallback(() => {
    const next = !ctrlArmedRef.current
    ctrlArmedRef.current = next
    setCtrlArmed(next)
  }, [])

  useEffect(() => {
    const el = container.current
    if (!el) return
    connectedRef.current = false
    setConnected(false)
    ctrlArmedRef.current = false
    setCtrlArmed(false)
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
    term.loadAddon(new ClipboardAddon(undefined, terminalClipboard))
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
      connectedRef.current = false
      setConnected(false)
      ctrlArmedRef.current = false
      setCtrlArmed(false)
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
        if (disposed || ws !== sock || suspendedRef.current) {
          sock.close()
          return
        }
        attempts = 0
        connectedRef.current = true
        setConnected(true)
      }
      sock.onmessage = (e) => term.write(new Uint8Array(e.data as ArrayBuffer))
      sock.onclose = (e) => {
        // A socket closed by park() may report after resume opened its successor.
        if (disposed || ws !== sock) return
        connectedRef.current = false
        setConnected(false)
        ctrlArmedRef.current = false
        setCtrlArmed(false)
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
      term.onData((d) => send(encoder.encode(consumeCtrl(d)))),
      term.onBinary((d) => send(Uint8Array.from(d, (c) => c.charCodeAt(0)))),
      term.onResize(({ cols, rows }) =>
        send(JSON.stringify({ type: "resize", cols, rows }))
      ),
    ]
    const terminalTextarea = el.querySelector("textarea")
    const markTerminalPaste = (event: ClipboardEvent) => {
      if (event.target !== terminalTextarea) return
      pasteEventPendingRef.current = true
      // xterm emits pasted data synchronously from its paste handler. Clear the
      // guard if the browser had no paste payload and therefore emitted no data.
      queueMicrotask(() => {
        pasteEventPendingRef.current = false
      })
    }
    document.addEventListener("paste", markTerminalPaste, true)
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
      document.removeEventListener("paste", markTerminalPaste, true)
      pasteEventPendingRef.current = false
      subs.forEach((s) => s.dispose())
      ws?.close()
      term.dispose()
    }
  }, [consumeCtrl, url])

  useEffect(() => {
    suspendedRef.current = suspended
    parkRef.current?.(suspended)
    if (suspended) {
      ctrlArmedRef.current = false
      setCtrlArmed(false)
    }
  }, [suspended])

  useEffect(() => {
    if (!active) {
      ctrlArmedRef.current = false
      setCtrlArmed(false)
      return
    }
    fitRef.current?.fit()
    termRef.current?.focus()
  }, [active])

  return (
    // No ScrollArea: xterm.js scrolls its own scrollback.
    <div
      className={cn(
        "flex size-full min-h-0 min-w-0 flex-col overflow-hidden bg-[#1c1917] p-2",
        !active && "hidden",
        className
      )}
    >
      <div ref={container} className="min-h-0 min-w-0 flex-1" />
      <TerminalControls
        disabled={!connected || suspended}
        ctrlArmed={ctrlArmed}
        onToggleCtrl={toggleCtrl}
        onInput={sendTerminalInput}
        onFocus={focusTerminal}
      />
    </div>
  )
}
