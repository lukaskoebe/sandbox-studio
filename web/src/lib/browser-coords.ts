/** The metadata that comes with a screencast frame, as agent-browser relays it. */
export type FrameMetadata = {
  /** The device viewport in DIP, which the whole frame shows. */
  deviceWidth: number
  deviceHeight: number
  /** DIP from the top of the frame to the top of the page, where browser top controls sit. */
  offsetTop: number
  /** Pinch zoom: DIP per CSS pixel. */
  pageScaleFactor: number
  /** How far the page is scrolled, in CSS pixels: a frame scrolled elsewhere shows other elements. */
  scrollOffsetX: number
  scrollOffsetY: number
}

/** A frame as it is painted: its pixel size and the metadata that came with it. */
export type DrawnFrame = {
  width: number
  height: number
  metadata: FrameMetadata
}

/**
 * Maps a pointer position to CSS pixels on the page, for input_mouse.
 *
 * The canvas draws the frame with object-fit: contain, so it is scaled to fit the element and
 * centred; the bars around it are not part of the page. A fraction (u, v) across the drawn frame
 * is the same fraction of the device viewport. The page starts offsetTop DIP down and is zoomed
 * by pageScaleFactor, so the CSS pixel is:
 *   x = u * deviceWidth / pageScaleFactor
 *   y = (v * deviceHeight - offsetTop) / pageScaleFactor
 * A point in the bars or above the page returns null. With `clamp`, it moves to the nearest edge
 * instead, so a drag's moves and release still reach the page when the pointer leaves the frame.
 */
export function pointerToPage(
  pointer: { x: number; y: number },
  box: { left: number; top: number; width: number; height: number },
  frame: DrawnFrame,
  clamp: boolean
): { x: number; y: number } | null {
  const { width, height, metadata: m } = frame
  const zoom = m.pageScaleFactor
  if (
    box.width <= 0 ||
    box.height <= 0 ||
    width <= 0 ||
    height <= 0 ||
    zoom <= 0
  )
    return null
  const fit = Math.min(box.width / width, box.height / height)
  const drawnWidth = width * fit
  const drawnHeight = height * fit
  const left = box.left + (box.width - drawnWidth) / 2
  const top = box.top + (box.height - drawnHeight) / 2
  const u = (pointer.x - left) / drawnWidth
  const v = (pointer.y - top) / drawnHeight
  const x = (u * m.deviceWidth) / zoom
  const y = (v * m.deviceHeight - m.offsetTop) / zoom
  const onPage = u >= 0 && u <= 1 && v >= 0 && v <= 1 && y >= 0
  if (!onPage && !clamp) return null
  const maxX = m.deviceWidth / zoom
  const maxY = (m.deviceHeight - m.offsetTop) / zoom
  return {
    x: Math.round(Math.min(Math.max(x, 0), maxX)),
    y: Math.round(Math.min(Math.max(y, 0), maxY)),
  }
}

/**
 * Maps a point in CSS pixels on the page to fractions (u, v) across and down the frame: the
 * inverse of pointerToPage, for drawing what the page reports, such as an element's box, over
 * the frame.
 */
export function pageToFrame(
  point: { x: number; y: number },
  m: FrameMetadata
): { u: number; v: number } {
  return {
    u: (point.x * m.pageScaleFactor) / m.deviceWidth,
    v: (point.y * m.pageScaleFactor + m.offsetTop) / m.deviceHeight,
  }
}
