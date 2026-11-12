import { useEffect, useRef, useState } from "react"

/**
 * A page size in CSS pixels, and its device pixels per CSS pixel, which its frames have:
 * missing is 1.
 */
export type PageSize = { width: number; height: number; scale?: number }

// The sizes Studio sets (internal/browser/viewport.go): the largest is the browser VM's screen.
const minWidth = 320
const minHeight = 200
const maxWidth = 3840
const maxHeight = 2160
const maxScale = 4

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
 * the largest size, and no smaller than the smallest. Its scale is the screen's, `ratio`, so
 * that the frames are as sharp as the screen shows them, but no more than keeps a frame within
 * the largest size, in hundredths, as Studio sets it (internal/browser/viewport.go).
 */
export function fitSize(width: number, height: number, ratio = 1): PageSize {
  const k = Math.min(1, maxWidth / width, maxHeight / height)
  const w = Math.min(maxWidth, Math.max(minWidth, Math.floor(width * k)))
  const h = Math.min(maxHeight, Math.max(minHeight, Math.floor(height * k)))
  const s = Math.min(ratio || 1, maxScale, maxWidth / w, maxHeight / h)
  return { width: w, height: h, scale: Math.max(1, Math.floor(s * 100) / 100) }
}

const near = (a: PageSize, b: PageSize) =>
  Math.abs(a.width - b.width) <= 1 &&
  Math.abs(a.height - b.height) <= 1 &&
  Math.abs((a.scale ?? 1) - (b.scale ?? 1)) < 0.02

/**
 * Asks for the page to be the size of `room`, less `reserved` pixels of its height, at this
 * screen's scale, while `enabled`: once the room settles, and again when the frames still have
 * another size a while later, as after the browser restarts. Only a window in use asks again,
 * so two views of one browser don't take turns. onFit answers with the scale the page got,
 * which a browser that can't take a scale leaves at 1, so it isn't asked again for more; nor is
 * one whose frames still have fewer pixels than the page by then.
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
  /** The page size of the frames the view gets, and their scale. */
  frame: PageSize | undefined
  enabled: boolean
  onFit: (size: PageSize) => Promise<number | undefined>
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
        setSize(
          fitSize(box.width, box.height - reserved, window.devicePixelRatio)
        )
      }, settle)
    }
    const observer = new ResizeObserver(measure)
    observer.observe(room)
    // A window moved to a screen of another density keeps its size in CSS pixels.
    let density: MediaQueryList | undefined
    const watchDensity = () => {
      density?.removeEventListener("change", onDensity)
      density = window.matchMedia(
        `(resolution: ${window.devicePixelRatio}dppx)`
      )
      density.addEventListener("change", onDensity)
    }
    const onDensity = () => {
      watchDensity()
      measure()
    }
    watchDensity()
    return () => {
      observer.disconnect()
      density?.removeEventListener("change", onDensity)
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
      // The frames have the page's size but fewer pixels than asked for, though the page took
      // the scale: that is what this browser's screencast sends, so asking again won't help.
      if (
        frame &&
        Math.abs(frame.width - size.width) <= 1 &&
        Math.abs(frame.height - size.height) <= 1 &&
        (frame.scale ?? 1) < (size.scale ?? 1)
      ) {
        asked.current = { ...last, scale: frame.scale }
        setSize((s) => (s === size ? { ...s, scale: frame.scale } : s))
        return
      }
    }
    asked.current = { ...size, at: Date.now() }
    void onFitRef.current(size).then((scale) => {
      // The page got less than this screen's scale: that is what its frames will have.
      if (scale === undefined || scale >= (size.scale ?? 1) - 0.005) return
      if (asked.current && near(asked.current, size))
        asked.current = { ...asked.current, scale }
      setSize((s) => (s === size ? { ...s, scale } : s))
    })
  }, [enabled, size, frame, check])
}
