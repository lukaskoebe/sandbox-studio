import { useCallback, useEffect, useRef, useState } from "react"
import { toast } from "sonner"
import { ClipboardTextIcon, CopyIcon } from "@phosphor-icons/react"
import { Terminal as XTerm } from "@xterm/xterm"
import { FitAddon } from "@xterm/addon-fit"
import { WebLinksAddon } from "@xterm/addon-web-links"
import { ClipboardAddon, type IClipboardProvider } from "@xterm/addon-clipboard"
import { WebglAddon } from "@xterm/addon-webgl"
import "@xterm/xterm/css/xterm.css"
import { TerminalControls } from "@/components/terminal-controls"
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuShortcut,
  ContextMenuTrigger,
} from "@/components/ui/context-menu"
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

const isMac =
  typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.platform)

/**
 * The terminal's side of OSC 52. tmux copies a mouse selection with an empty clipboard name
 * ("52;;…"), which xterm treats as the clipboard, so every name copies. Reads are refused: a
 * program in the sandbox must not read the host's clipboard.
 */
function terminalClipboard(onCopied: () => void): IClipboardProvider {
  return {
    readText: () => "",
    writeText(_selection, text) {
      if (!text) return undefined
      // Best effort: a denied browser clipboard write must not surface as a terminal error.
      return copyText(text).then(onCopied, () => undefined)
    },
  }
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
  // What the right-click menu acts on: xterm's own selection when it opened (Shift-drag, or
  // Option-drag on a Mac; a plain drag selects in tmux, which copies on release).
  const [menuSelection, setMenuSelection] = useState("")
  const [copied, setCopied] = useState(false)
  const copiedTimer = useRef<ReturnType<typeof setTimeout>>(undefined)
  const showCopied = useCallback(() => {
    setCopied(true)
    clearTimeout(copiedTimer.current)
    copiedTimer.current = setTimeout(() => setCopied(false), 1500)
  }, [])
  useEffect(() => () => clearTimeout(copiedTimer.current), [])

  const copySelection = useCallback(() => {
    const text = termRef.current?.getSelection() ?? ""
    if (!text) return false
    copyText(text).then(showCopied, () =>
      toast.error("Could not copy", {
        description: "The browser did not allow access to the clipboard.",
      })
    )
    return true
  }, [showCopied])

  const pasteClipboard = useCallback(async () => {
    const term = termRef.current
    if (!term || !connectedRef.current || suspendedRef.current) return
    try {
      const clipboard = (navigator as Partial<Navigator>).clipboard
      if (typeof clipboard?.readText !== "function") throw new Error()
      const text = await clipboard.readText()
      if (text) term.paste(text)
      term.focus()
    } catch {
      // Reading the clipboard needs permission and a secure page; the paste key always works.
      toast("Paste with the keyboard", {
        description: `The browser did not allow reading the clipboard here. Press ${isMac ? "⌘V" : "Ctrl+V"} in the terminal instead.`,
      })
      term.focus()
    }
  }, [])

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
    term.loadAddon(new ClipboardAddon(undefined, terminalClipboard(showCopied)))
    // Ctrl+Shift+C copies xterm's selection, as in desktop terminals; Ctrl+C stays the
    // interrupt. On a Mac, ⌘C copies through the browser's copy event, which xterm handles.
    term.attachCustomKeyEventHandler((e) => {
      if (
        e.type === "keydown" &&
        !isMac &&
        e.ctrlKey &&
        e.shiftKey &&
        e.code === "KeyC"
      ) {
        e.preventDefault()
        copySelection()
        return false
      }
      return true
    })
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
    // The right button opens Studio's menu, not tmux's: tmux would draw a menu of its own
    // under the browser's.
    const keepRightButton = (event: MouseEvent) => {
      if (event.button === 2) event.stopPropagation()
    }
    el.addEventListener("mousedown", keepRightButton, true)
    el.addEventListener("mouseup", keepRightButton, true)
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
      el.removeEventListener("mousedown", keepRightButton, true)
      el.removeEventListener("mouseup", keepRightButton, true)
      pasteEventPendingRef.current = false
      subs.forEach((s) => s.dispose())
      ws?.close()
      term.dispose()
    }
  }, [consumeCtrl, copySelection, showCopied, url])

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
        "relative flex size-full min-h-0 min-w-0 flex-col overflow-hidden bg-[#1c1917] p-2",
        !active && "hidden",
        className
      )}
    >
      <ContextMenu
        onOpenChange={(open) => {
          if (open) setMenuSelection(termRef.current?.getSelection() ?? "")
          else termRef.current?.focus()
        }}
      >
        <ContextMenuTrigger
          ref={container}
          className="relative min-h-0 min-w-0 flex-1"
          aria-label="Terminal"
        />
        <ContextMenuContent>
          <ContextMenuItem disabled={!menuSelection} onClick={copySelection}>
            <CopyIcon />
            Copy
            {!isMac && <ContextMenuShortcut>Ctrl+Shift+C</ContextMenuShortcut>}
          </ContextMenuItem>
          <ContextMenuItem
            disabled={!connected || suspended}
            onClick={() => void pasteClipboard()}
          >
            <ClipboardTextIcon />
            Paste
            <ContextMenuShortcut>{isMac ? "⌘V" : "Ctrl+V"}</ContextMenuShortcut>
          </ContextMenuItem>
          <ContextMenuSeparator />
          <p className="max-w-56 px-2 py-1 text-[0.6875rem] text-muted-foreground">
            Drag to select: it is copied when you let go.
          </p>
        </ContextMenuContent>
      </ContextMenu>
      {copied && (
        <span
          role="status"
          className="pointer-events-none absolute top-3 right-4 rounded-md bg-stone-700 px-2 py-0.5 text-xs text-stone-100 shadow"
        >
          Copied
        </span>
      )}
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
