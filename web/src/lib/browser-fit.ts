import { useEffect, useRef, useState } from "react"

/** A page size in CSS pixels. */
export type PageSize = { width: number; height: number }

// The sizes Studio sets (internal/browser/viewport.go): the largest is the browser VM's screen.
const minWidth = 320
const minHeight = 200
const maxWidth = 3840
const maxHeight = 2160

/** agent-browser's own page size, which the page goes back to when the view stops fitting it. */
export const defaultPageSize: PageSize = { width: 1280, height: 720 }

// How long a fit waits for the room to stop changing, as while a panel is dragged, and how
// long before it asks again when the frames still have another size.
const settle = 300
const retry = 5000

const key = "studio.browser.fit"

/** Whether this browser fits a teammate's page to the live view. */
export function fitPreferred() {
  try {
    return localStorage.getItem(key) === "1"
  } catch {
    return false
  }
}

export function setFitPreferred(on: boolean) {
  try {
    if (on) localStorage.setItem(key, "1")
    else localStorage.removeItem(key)
  } catch {
    // Without storage, the next visit doesn't fit until asked again.
  }
}

/**
 * The page size for a room: the room itself, in whole pixels, or one of its shape that fits
 * the largest size, and no smaller than the smallest.
 */
export function fitSize(width: number, height: number): PageSize {
  const k = Math.min(1, maxWidth / width, maxHeight / height)
  return {
    width: Math.min(maxWidth, Math.max(minWidth, Math.floor(width * k))),
    height: Math.min(maxHeight, Math.max(minHeight, Math.floor(height * k))),
  }
}

const near = (a: PageSize, b: PageSize) =>
  Math.abs(a.width - b.width) <= 1 && Math.abs(a.height - b.height) <= 1

/**
 * Asks for the page to be the size of `room`, less `reserved` pixels of its height, while
 * `enabled`: once the room settles, and again when the frames still have another size a while
 * later, as after the browser restarts. Only a window in use asks again, so two views of one
 * browser don't take turns.
 */
export function useFitPage({
  room,
  reserved,
  frame,
  enabled,
  onFit,
}: {
  room: HTMLElement | null | undefined
  reserved: number
  /** The size of the frames the view gets. */
  frame: PageSize | undefined
  enabled: boolean
  onFit: (size: PageSize) => void
}) {
  const [size, setSize] = useState<PageSize>()
  const asked = useRef<PageSize & { at: number }>(undefined)
  const onFitRef = useRef(onFit)
  useEffect(() => {
    onFitRef.current = onFit
  })
  const [check, setCheck] = useState(0)

  useEffect(() => {
    if (!room || !enabled) return
    let timer: ReturnType<typeof setTimeout> | undefined
    const measure = () => {
      clearTimeout(timer)
      timer = setTimeout(() => {
        const box = room.getBoundingClientRect()
        if (box.width < 1 || box.height - reserved < 1) return
        setSize(fitSize(box.width, box.height - reserved))
      }, settle)
    }
    const observer = new ResizeObserver(measure)
    observer.observe(room)
    return () => {
      observer.disconnect()
      clearTimeout(timer)
    }
  }, [room, reserved, enabled])

  useEffect(() => {
    if (!enabled) return
    const recheck = () => setCheck((n) => n + 1)
    window.addEventListener("focus", recheck)
    document.addEventListener("visibilitychange", recheck)
    return () => {
      window.removeEventListener("focus", recheck)
      document.removeEventListener("visibilitychange", recheck)
    }
  }, [enabled])

  useEffect(() => {
    if (!enabled || !size || document.visibilityState !== "visible") return
    if (frame && near(frame, size)) return
    const last = asked.current
    if (last && near(last, size)) {
      const wait = last.at + retry - Date.now()
      if (wait > 0) {
        const t = setTimeout(() => setCheck((n) => n + 1), wait)
        return () => clearTimeout(t)
      }
      if (!document.hasFocus()) return
    }
    asked.current = { ...size, at: Date.now() }
    onFitRef.current(size)
  }, [enabled, size, frame, check])
}
