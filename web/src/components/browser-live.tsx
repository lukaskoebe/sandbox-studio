import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
} from "react"
import { cn } from "@/lib/utils"
import {
  pointerToPage,
  type DrawnFrame,
  type FrameMetadata,
} from "@/lib/browser-coords"

// Chrome accepts CDP char text one Unicode code point at a time.
const mobileInputSentinel = "\u200b"

function splitKeyboardText(text: string) {
  return Array.from(text)
}

const mobileKeys = [
  { label: "Esc", key: "Escape", code: "Escape", vk: 27 },
  { label: "Tab", key: "Tab", code: "Tab", vk: 9 },
  { label: "←", key: "ArrowLeft", code: "ArrowLeft", vk: 37 },
  { label: "↑", key: "ArrowUp", code: "ArrowUp", vk: 38 },
  { label: "↓", key: "ArrowDown", code: "ArrowDown", vk: 40 },
  { label: "→", key: "ArrowRight", code: "ArrowRight", vk: 39 },
  { label: "⌫", key: "Backspace", code: "Backspace", vk: 8 },
  { label: "↵", key: "Enter", code: "Enter", vk: 13 },
] as const

const specialMobileKeys = new Set([
  "Backspace",
  "Delete",
  "Enter",
  "Tab",
  "Escape",
  "ArrowLeft",
  "ArrowRight",
  "ArrowUp",
  "ArrowDown",
  "Home",
  "End",
  "PageUp",
  "PageDown",
  "Insert",
])
const modifierKeys = new Set(["Control", "Alt", "Shift", "Meta"])

/**
 * The live view of a teammate's browser: agent-browser's screencast, relayed by Studio over
 * a WebSocket, drawn on a canvas. Each frame is {"type":"frame","data":<base64 JPEG>,
 * "metadata":{deviceWidth,deviceHeight,...}}. While `driving`, mouse and keyboard events go
 * back as input_mouse and input_keyboard messages; Studio drops them otherwise.
 */
function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value)
}

// The frame's metadata, with the frame's own size and no offset or zoom where it is missing.
function frameMetadata(
  value: unknown,
  width: number,
  height: number
): FrameMetadata {
  // verify (live): deviceWidth/Height and offsetTop are DIP, pageScaleFactor is DIP per CSS px.
  const m = isRecord(value) ? value : {}
  const field = (v: unknown, fallback: number, positive = false) =>
    typeof v === "number" && Number.isFinite(v) && (!positive || v > 0)
      ? v
      : fallback
  return {
    deviceWidth: field(m.deviceWidth, width, true),
    deviceHeight: field(m.deviceHeight, height, true),
    offsetTop: field(m.offsetTop, 0),
    pageScaleFactor: field(m.pageScaleFactor, 1, true),
  }
}

// Wheel deltas arrive in pixels, lines (16 px each) or pages (one viewport height).
function wheelPixels(delta: number, mode: number) {
  if (mode === WheelEvent.DOM_DELTA_LINE) return delta * 16
  if (mode === WheelEvent.DOM_DELTA_PAGE) return delta * window.innerHeight
  return delta
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
  const mobileInput = useRef<HTMLTextAreaElement>(null)
  const mobileBeforeInputHandler = useRef<((event: InputEvent) => void) | null>(
    null
  )
  const socket = useRef<WebSocket | null>(null)
  const activeSocketURL = useRef<string | undefined>(undefined)
  // The frame on the canvas, with its own metadata: clicks map through it, not a newer frame's.
  const drawnFrame = useRef<DrawnFrame | undefined>(undefined)
  // The button a press holds: a move while it is held is a drag, and its release names it.
  const heldButton = useRef<string | undefined>(undefined)
  const onPageURLRef = useRef(onPageURL)
  const activePageURLRef = useRef(pageURL)
  const composing = useRef(false)
  const compositionEcho = useRef("")
  const compositionEchoTimer = useRef<
    ReturnType<typeof setTimeout> | undefined
  >(undefined)
  const handledKeyUps = useRef(new Set<string>())
  const pendingDeletionKeydown = useRef(false)
  const pendingDeletionInput = useRef(0)
  const deletionKeydownTimer = useRef<
    ReturnType<typeof setTimeout> | undefined
  >(undefined)
  const deletionInputTimer = useRef<ReturnType<typeof setTimeout> | undefined>(
    undefined
  )
  const pendingEnterKeydown = useRef(false)
  const pendingEnterInput = useRef(0)
  const enterKeydownTimer = useRef<ReturnType<typeof setTimeout> | undefined>(
    undefined
  )
  const enterInputTimer = useRef<ReturnType<typeof setTimeout> | undefined>(
    undefined
  )
  const controlLatched = useRef(false)
  const [state, setState] = useState<"connecting" | "live" | "closed">(
    "connecting"
  )
  const [notice, setNotice] = useState("")
  const [activePageURL, setActivePageURL] = useState(pageURL)
  const [renderedPageURL, setRenderedPageURL] = useState<string>()
  const [hasRenderedFrame, setHasRenderedFrame] = useState(false)
  const [keyboardShown, setKeyboardShown] = useState(false)
  const [controlPressed, setControlPressed] = useState(false)
  const [automaticKeyboardControls, setAutomaticKeyboardControls] =
    useState(false)
  const [desktopControlsOpen, setDesktopControlsOpen] = useState(false)
  const canDrive = driving && inputEnabled

  const primeMobileInput = (input: HTMLTextAreaElement | null) => {
    if (!input) return
    input.value = canDrive && state === "live" ? mobileInputSentinel : ""
    input.setSelectionRange(input.value.length, input.value.length)
  }

  const scheduleDeletionKeydownReset = () => {
    if (deletionKeydownTimer.current) clearTimeout(deletionKeydownTimer.current)
    deletionKeydownTimer.current = setTimeout(() => {
      pendingDeletionKeydown.current = false
      deletionKeydownTimer.current = undefined
    }, 50)
  }

  const scheduleDeletionInputReset = () => {
    if (deletionInputTimer.current) clearTimeout(deletionInputTimer.current)
    deletionInputTimer.current = setTimeout(() => {
      pendingDeletionInput.current = 0
      deletionInputTimer.current = undefined
    }, 250)
  }

  const scheduleEnterKeydownReset = () => {
    if (enterKeydownTimer.current) clearTimeout(enterKeydownTimer.current)
    enterKeydownTimer.current = setTimeout(() => {
      pendingEnterKeydown.current = false
      enterKeydownTimer.current = undefined
    }, 50)
  }

  const scheduleEnterInputReset = () => {
    if (enterInputTimer.current) clearTimeout(enterInputTimer.current)
    enterInputTimer.current = setTimeout(() => {
      pendingEnterInput.current = 0
      enterInputTimer.current = undefined
    }, 250)
  }

  const clearMobileBuffer = useCallback(() => {
    if (compositionEchoTimer.current) clearTimeout(compositionEchoTimer.current)
    if (deletionKeydownTimer.current) clearTimeout(deletionKeydownTimer.current)
    if (deletionInputTimer.current) clearTimeout(deletionInputTimer.current)
    if (enterKeydownTimer.current) clearTimeout(enterKeydownTimer.current)
    if (enterInputTimer.current) clearTimeout(enterInputTimer.current)
    compositionEchoTimer.current = undefined
    deletionKeydownTimer.current = undefined
    deletionInputTimer.current = undefined
    enterKeydownTimer.current = undefined
    enterInputTimer.current = undefined
    compositionEcho.current = ""
    composing.current = false
    handledKeyUps.current.clear()
    pendingDeletionKeydown.current = false
    pendingDeletionInput.current = 0
    pendingEnterKeydown.current = false
    pendingEnterInput.current = 0
    if (mobileInput.current) mobileInput.current.value = ""
  }, [])

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
    const query = window.matchMedia("(max-width: 767px), (any-pointer: coarse)")
    const update = () => setAutomaticKeyboardControls(query.matches)
    update()
    query.addEventListener("change", update)
    return () => query.removeEventListener("change", update)
  }, [])

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
      activeSocketURL.current = url
      setState("connecting")
      setNotice("")
      setRenderedPageURL(undefined)
      setHasRenderedFrame(false)
      drawnFrame.current = undefined
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
        // The metadata is applied when this frame is painted, not when it arrives.
        const metadataValue = parsed.metadata
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
          drawnFrame.current = {
            width: img.width,
            height: img.height,
            metadata: frameMetadata(metadataValue, img.width, img.height),
          }
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
        activeSocketURL.current = undefined
        clearMobileBuffer()
        mobileInput.current?.blur()
        controlLatched.current = false
        heldButton.current = undefined
        setKeyboardShown(false)
        setControlPressed(false)
        setDesktopControlsOpen(false)
        setState("closed")
        setNotice(ev.reason || "The live view disconnected")
        if (!stopped) retry = setTimeout(connect, 3000)
      }
    }
    connect()
    return () => {
      stopped = true
      clearTimeout(retry)
      clearMobileBuffer()
      mobileInput.current?.blur()
      controlLatched.current = false
      heldButton.current = undefined
      const ws = activeSocket
      activeSocket = null
      if (socket.current === ws) {
        socket.current = null
        activeSocketURL.current = undefined
      }
      ws?.close()
    }
  }, [url, clearMobileBuffer])

  useEffect(() => {
    if (canDrive && state === "live") return
    clearMobileBuffer()
    mobileInput.current?.blur()
    controlLatched.current = false
    heldButton.current = undefined
    setKeyboardShown(false)
    setControlPressed(false)
    setDesktopControlsOpen(false)
  }, [canDrive, state, clearMobileBuffer])

  const send = (msg: object) => {
    const ws = socket.current
    if (
      !canDrive ||
      activeSocketURL.current !== url ||
      ws?.readyState !== WebSocket.OPEN
    )
      return false
    ws.send(JSON.stringify(msg))
    return true
  }
  // CDP modifier bits: Alt 1, Control 2, Meta 4, Shift 8.
  const modifiers = (e: React.MouseEvent | React.KeyboardEvent) =>
    (e.altKey ? 1 : 0) |
    (e.ctrlKey ? 2 : 0) |
    (e.metaKey ? 4 : 0) |
    (e.shiftKey ? 8 : 0)
  // The page point under a pointer, through the drawn frame; null when it is not on the page.
  const pagePoint = (e: React.MouseEvent, clamp: boolean) => {
    const frame = drawnFrame.current
    if (!frame) return null
    return pointerToPage(
      { x: e.clientX, y: e.clientY },
      e.currentTarget.getBoundingClientRect(),
      frame,
      clamp
    )
  }
  const buttons = ["left", "middle", "right", "back", "forward"]
  const mouse = (
    eventType: string,
    e: React.MouseEvent,
    button: string,
    clamp = false,
    extra = {}
  ) => {
    const at = pagePoint(e, clamp)
    return (
      !!at &&
      send({
        type: "input_mouse",
        eventType,
        ...at,
        button,
        clickCount: eventType === "mouseMoved" ? 0 : Math.max(1, e.detail),
        modifiers: modifiers(e),
        ...extra,
      })
    )
  }
  // Ends a press that was sent. Its release clamps to the frame, so the page is not left pressed
  // when the pointer has been dragged out of the canvas. Returns whether the release was sent.
  const release = (e: React.PointerEvent<HTMLCanvasElement>) => {
    const button = heldButton.current
    if (!e.isPrimary || button === undefined) return false
    heldButton.current = undefined
    return mouse("mouseReleased", e, button, true)
  }
  const lastMove = useRef(0)
  const keyboardEvent = (
    eventType: "keyDown" | "keyUp",
    key: string,
    code: string,
    windowsVirtualKeyCode: number,
    keyModifiers: number,
    text?: string
  ) =>
    send({
      type: "input_keyboard",
      eventType,
      key,
      code,
      text,
      windowsVirtualKeyCode,
      modifiers: keyModifiers,
    })

  const pressKey = (
    key: string,
    code: string,
    windowsVirtualKeyCode: number,
    keyModifiers = controlLatched.current ? 2 : 0
  ) => {
    // Native agent-browser forwards text to CDP; Enter uses CR for input fields.
    if (
      !keyboardEvent(
        "keyDown",
        key,
        code,
        windowsVirtualKeyCode,
        keyModifiers,
        key === "Enter" ? "\r" : undefined
      )
    )
      return false
    keyboardEvent("keyUp", key, code, windowsVirtualKeyCode, keyModifiers)
    return true
  }

  const key = (eventType: string, e: React.KeyboardEvent) => {
    if (!canDrive) return
    e.preventDefault()
    const keyID = e.code || e.key
    if (eventType === "keyUp" && handledKeyUps.current.delete(keyID)) return
    if (eventType === "keyDown" && controlLatched.current) {
      const sent = pressKey(e.key, e.code, e.keyCode, modifiers(e) | 2)
      if (sent) {
        handledKeyUps.current.add(keyID)
        controlLatched.current = false
        setControlPressed(false)
      }
      return
    }
    send({
      type: "input_keyboard",
      eventType,
      key: e.key,
      code: e.code,
      text:
        eventType === "keyDown"
          ? e.key === "Enter"
            ? "\r"
            : e.key.length === 1 && !e.ctrlKey && !e.metaKey && !e.altKey
              ? e.key
              : undefined
          : undefined,
      windowsVirtualKeyCode: e.keyCode,
      modifiers: modifiers(e),
    })
  }

  const consumeControl = () => {
    controlLatched.current = false
    setControlPressed(false)
  }

  const sendDeletion = (backspace: boolean) => {
    const sent = pressKey(
      backspace ? "Backspace" : "Delete",
      backspace ? "Backspace" : "Delete",
      backspace ? 8 : 46
    )
    if (sent && controlLatched.current) consumeControl()
    return sent
  }

  const sendEnter = () => {
    const sent = pressKey("Enter", "Enter", 13)
    if (sent && controlLatched.current) consumeControl()
    return sent
  }

  const sendText = (text: string, allowShortcut = true) => {
    if (!text || !canDrive || state !== "live") return
    if (controlLatched.current) {
      if (allowShortcut) {
        if (/^[a-z]$/i.test(text)) {
          const letter = text.toUpperCase()
          pressKey(text, `Key${letter}`, letter.charCodeAt(0), 2)
        }
        consumeControl()
        return
      }
      consumeControl()
    }
    const characters = splitKeyboardText(text)
    for (let index = 0; index < characters.length; index += 1) {
      const character = characters[index]
      if (character === "\r" || character === "\n") {
        if (character === "\r" && characters[index + 1] === "\n") index += 1
        if (!sendEnter()) return
        continue
      }
      if (
        !send({
          type: "input_keyboard",
          eventType: "char",
          text: character,
          windowsVirtualKeyCode: 0,
          modifiers: 0,
        })
      )
        return
    }
  }

  const mobileKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (composing.current || e.nativeEvent.isComposing) return
    if (!canDrive || state !== "live") {
      e.preventDefault()
      clearMobileBuffer()
      return
    }
    if (compositionEchoTimer.current) {
      clearTimeout(compositionEchoTimer.current)
      compositionEchoTimer.current = undefined
      compositionEcho.current = ""
    }

    const keyID = e.code || e.key
    const keyModifiers = modifiers(e) | (controlLatched.current ? 2 : 0)
    if (modifierKeys.has(e.key)) {
      e.preventDefault()
      keyboardEvent("keyDown", e.key, e.code, e.keyCode, keyModifiers)
      return
    }
    const pasteShortcut =
      e.key.toLowerCase() === "v" &&
      (e.ctrlKey || e.metaKey || controlLatched.current)
    if (pasteShortcut) return
    const shortcut =
      e.ctrlKey || e.metaKey || e.altKey || controlLatched.current
    if (specialMobileKeys.has(e.key) || shortcut) {
      e.preventDefault()
      const sent = pressKey(e.key, e.code, e.keyCode, keyModifiers)
      if (sent) {
        handledKeyUps.current.add(keyID)
        if (e.key === "Backspace" || e.key === "Delete") {
          pendingDeletionKeydown.current = true
          scheduleDeletionKeydownReset()
        }
        if (e.key === "Enter") {
          pendingEnterKeydown.current = true
          scheduleEnterKeydownReset()
        }
      }
      if (controlLatched.current) consumeControl()
    }
  }

  const mobileKeyUp = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    const keyID = e.code || e.key
    if (handledKeyUps.current.delete(keyID)) {
      e.preventDefault()
      return
    }
    if (modifierKeys.has(e.key)) {
      e.preventDefault()
      keyboardEvent(
        "keyUp",
        e.key,
        e.code,
        e.keyCode,
        modifiers(e) | (controlLatched.current ? 2 : 0)
      )
    }
  }

  const mobileBeforeInput = (nativeEvent: InputEvent) => {
    const input = mobileInput.current
    if (!input) return
    if (composing.current || nativeEvent.isComposing) return
    if (!canDrive || state !== "live") {
      nativeEvent.preventDefault()
      clearMobileBuffer()
      return
    }

    if (
      nativeEvent.inputType === "deleteContentBackward" ||
      nativeEvent.inputType === "deleteContentForward"
    ) {
      nativeEvent.preventDefault()
      const pairedWithKeydown = pendingDeletionKeydown.current
      pendingDeletionKeydown.current = false
      if (!pairedWithKeydown)
        sendDeletion(nativeEvent.inputType === "deleteContentBackward")
      if (!nativeEvent.cancelable) {
        pendingDeletionInput.current += 1
        scheduleDeletionInputReset()
      }
      primeMobileInput(input)
      return
    }

    if (
      nativeEvent.inputType === "insertLineBreak" ||
      nativeEvent.inputType === "insertParagraph"
    ) {
      nativeEvent.preventDefault()
      const pairedWithKeydown = pendingEnterKeydown.current
      pendingEnterKeydown.current = false
      if (!pairedWithKeydown) sendEnter()
      if (!nativeEvent.cancelable) {
        pendingEnterInput.current += 1
        scheduleEnterInputReset()
      }
      primeMobileInput(input)
    }
  }

  const mobileInputChanged = (e: React.FormEvent<HTMLTextAreaElement>) => {
    const input = e.currentTarget
    const nativeEvent = e.nativeEvent as InputEvent
    if (composing.current || nativeEvent.isComposing) return

    const text = input.value.startsWith(mobileInputSentinel)
      ? input.value.slice(mobileInputSentinel.length)
      : input.value
    if (compositionEcho.current) {
      const echo = compositionEcho.current
      compositionEcho.current = ""
      if (text === echo) {
        primeMobileInput(input)
        return
      }
    }

    if (
      nativeEvent.inputType === "deleteContentBackward" ||
      nativeEvent.inputType === "deleteContentForward"
    ) {
      const pendingInput = pendingDeletionInput.current > 0
      if (pendingInput) {
        pendingDeletionInput.current -= 1
        if (!pendingDeletionInput.current && deletionInputTimer.current) {
          clearTimeout(deletionInputTimer.current)
          deletionInputTimer.current = undefined
        }
      }
      const pairedWithKeydown = pendingDeletionKeydown.current
      pendingDeletionKeydown.current = false
      if (!pendingInput && !pairedWithKeydown)
        sendDeletion(nativeEvent.inputType === "deleteContentBackward")
      primeMobileInput(input)
      return
    }
    if (
      nativeEvent.inputType === "insertLineBreak" ||
      nativeEvent.inputType === "insertParagraph"
    ) {
      const pendingInput = pendingEnterInput.current > 0
      if (pendingInput) {
        pendingEnterInput.current -= 1
        if (!pendingEnterInput.current && enterInputTimer.current) {
          clearTimeout(enterInputTimer.current)
          enterInputTimer.current = undefined
        }
      }
      const pairedWithKeydown = pendingEnterKeydown.current
      pendingEnterKeydown.current = false
      if (!pendingInput && !pairedWithKeydown) sendEnter()
      primeMobileInput(input)
      return
    }
    if (text) sendText(text, nativeEvent.inputType !== "insertFromPaste")
    primeMobileInput(input)
  }

  const mobileCompositionEnd = (
    e: React.CompositionEvent<HTMLTextAreaElement>
  ) => {
    composing.current = false
    const value = e.currentTarget.value
    const committed = e.data
      ? e.data
      : value.startsWith(mobileInputSentinel)
        ? value.slice(mobileInputSentinel.length)
        : value
    primeMobileInput(e.currentTarget)
    if (!committed) return
    sendText(committed, false)
    compositionEcho.current = committed
    if (compositionEchoTimer.current) clearTimeout(compositionEchoTimer.current)
    compositionEchoTimer.current = setTimeout(() => {
      compositionEcho.current = ""
      compositionEchoTimer.current = undefined
    }, 250)
  }

  const pasteText = (e: React.ClipboardEvent<HTMLTextAreaElement>) => {
    e.preventDefault()
    const text = e.clipboardData.getData("text/plain")
    primeMobileInput(e.currentTarget)
    sendText(text, false)
  }

  const focusMobileInput = () => {
    if (!canDrive || state !== "live" || activeSocketURL.current !== url) return
    primeMobileInput(mobileInput.current)
    mobileInput.current?.focus({ preventScroll: true })
    setKeyboardShown(true)
  }

  const pressMobileKey = (mobileKey: (typeof mobileKeys)[number]) => {
    if (!canDrive || state !== "live") return
    if (
      pressKey(mobileKey.key, mobileKey.code, mobileKey.vk) &&
      controlLatched.current
    )
      consumeControl()
  }

  useLayoutEffect(() => {
    mobileBeforeInputHandler.current = mobileBeforeInput
  })

  useLayoutEffect(() => {
    const input = mobileInput.current
    if (!input) return
    const handleBeforeInput = (event: InputEvent) =>
      mobileBeforeInputHandler.current?.(event)
    input.addEventListener("beforeinput", handleBeforeInput)
    return () => input.removeEventListener("beforeinput", handleBeforeInput)
  }, [])

  return (
    <div className={cn("relative bg-muted", className)}>
      <canvas
        ref={canvas}
        tabIndex={canDrive ? 0 : -1}
        aria-label="Live view of the browser"
        className={cn(
          "block h-auto w-full touch-none outline-none",
          canDrive
            ? "cursor-default ring-2 ring-primary"
            : "pointer-events-none",
          state !== "live" && "opacity-40"
        )}
        onPointerDown={(e) => {
          if (!e.isPrimary) return
          // Capture keeps moves and the release coming when the pointer leaves the canvas.
          e.currentTarget.setPointerCapture(e.pointerId)
          if (e.pointerType === "mouse") e.currentTarget.focus()
          else e.preventDefault()
          const button = buttons[e.button]
          if (mouse("mousePressed", e, button)) heldButton.current = button
        }}
        onPointerMove={(e) => {
          if (!e.isPrimary) return
          const now = performance.now()
          if (now - lastMove.current < 50) return
          lastMove.current = now
          const held = heldButton.current
          mouse("mouseMoved", e, held ?? "none", held !== undefined)
        }}
        onPointerUp={(e) => {
          // A touch tap focuses the hidden input once its release is sent: focusing earlier
          // opens the keyboard, which shifts the layout before the release is mapped.
          if (release(e) && e.pointerType !== "mouse") focusMobileInput()
        }}
        onPointerCancel={release}
        onWheel={(e) =>
          mouse("mouseWheel", e, buttons[e.button], false, {
            deltaX: wheelPixels(e.deltaX, e.deltaMode),
            deltaY: wheelPixels(e.deltaY, e.deltaMode),
          })
        }
        onContextMenu={(e) => canDrive && e.preventDefault()}
        onKeyDown={(e) => key("keyDown", e)}
        onKeyUp={(e) => key("keyUp", e)}
      />
      <textarea
        ref={mobileInput}
        aria-label="Type into the remote browser"
        autoCapitalize="off"
        autoComplete="off"
        autoCorrect="off"
        className="pointer-events-none absolute top-0 left-0 h-px w-px resize-none overflow-hidden border-0 bg-transparent p-0 text-base opacity-0"
        disabled={!canDrive || state !== "live"}
        rows={1}
        spellCheck={false}
        tabIndex={-1}
        onFocus={() => setKeyboardShown(true)}
        onBlur={() => {
          clearMobileBuffer()
          setKeyboardShown(false)
        }}
        onKeyDown={mobileKeyDown}
        onKeyUp={mobileKeyUp}
        onInput={mobileInputChanged}
        onPaste={pasteText}
        onCompositionStart={() => {
          composing.current = true
          compositionEcho.current = ""
          if (compositionEchoTimer.current)
            clearTimeout(compositionEchoTimer.current)
          compositionEchoTimer.current = undefined
        }}
        onCompositionEnd={mobileCompositionEnd}
      />
      {canDrive && state === "live" && (
        <div className="flex flex-wrap items-center gap-2 border-t bg-background p-2">
          {!automaticKeyboardControls && (
            <button
              type="button"
              aria-expanded={desktopControlsOpen}
              className="min-h-9 rounded-md border border-input px-2 text-xs font-medium hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring"
              onClick={() => {
                if (desktopControlsOpen && keyboardShown) {
                  mobileInput.current?.blur()
                  clearMobileBuffer()
                  setKeyboardShown(false)
                }
                setDesktopControlsOpen((open) => !open)
              }}
            >
              {desktopControlsOpen
                ? "Hide keyboard controls"
                : "Keyboard controls"}
            </button>
          )}
          {(automaticKeyboardControls || desktopControlsOpen) && (
            <div
              aria-label="Remote browser keyboard controls"
              className="flex flex-wrap items-center gap-1"
              role="group"
            >
              <button
                type="button"
                aria-pressed={keyboardShown}
                className="min-h-9 rounded-md border border-input px-2 text-xs font-medium hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring"
                onPointerDown={(e) => e.preventDefault()}
                onClick={() => {
                  if (keyboardShown) {
                    mobileInput.current?.blur()
                    clearMobileBuffer()
                    setKeyboardShown(false)
                  } else {
                    focusMobileInput()
                  }
                }}
              >
                {keyboardShown ? "Hide keyboard" : "Keyboard"}
              </button>
              <button
                type="button"
                aria-label="Control modifier"
                aria-pressed={controlPressed}
                className="min-h-9 min-w-9 rounded-md border border-input px-2 text-xs font-medium hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring aria-pressed:bg-primary/10"
                onPointerDown={(e) => e.preventDefault()}
                onClick={() => {
                  const next = !controlLatched.current
                  controlLatched.current = next
                  setControlPressed(next)
                }}
              >
                Ctrl
              </button>
              {mobileKeys.map((mobileKey) => (
                <button
                  key={mobileKey.code}
                  type="button"
                  aria-label={mobileKey.key}
                  className="min-h-9 min-w-9 rounded-md border border-input px-2 text-xs font-medium hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring"
                  onPointerDown={(e) => e.preventDefault()}
                  onClick={() => pressMobileKey(mobileKey)}
                >
                  {mobileKey.label}
                </button>
              ))}
            </div>
          )}
        </div>
      )}
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
