import type { Sandbox } from "@/lib/api/client"
import type { components } from "@/lib/api/schema.gen"
import { pageToFrame, type FrameMetadata } from "@/lib/browser-coords"
import type { Chat } from "@/lib/chats"

/** The element under a note, as the page described it to Studio (docs/browser.md). */
export type NoteElement = components["schemas"]["Element"]

/**
 * A note the user pinned on the live view with a right-click. It sits at (u, v), fractions of
 * the frame across and down, so it stays on its spot as the view resizes; x and y are the
 * same point in CSS pixels from the top left of the page's viewport, for the agent. url is
 * the page the frame showed, and view the frame's metadata, which maps the element's box onto
 * it. fresh says that the page still showed the frame, so it could be asked for the element.
 */
export type BrowserNote = {
  id: number
  u: number
  v: number
  x: number
  y: number
  url?: string
  view: FrameMetadata
  fresh: boolean
  text: string
  element?: NoteElement
}

/** A spot on the live view: where a right-click landed, before it has a note. */
export type NoteSpot = Omit<BrowserNote, "id" | "text" | "element">

/**
 * The element's box in fractions of the frame, kept within it. An element with no area on the
 * frame has none, nor does one that covers most of it, such as the page's body: a box around
 * the whole page shows nothing.
 */
export function elementBox(note: BrowserNote) {
  const box = note.element?.box
  if (!box || box.width <= 0 || box.height <= 0) return undefined
  const a = pageToFrame(box, note.view)
  const b = pageToFrame(
    { x: box.x + box.width, y: box.y + box.height },
    note.view
  )
  const clamp = (v: number) => Math.min(Math.max(v, 0), 1)
  const left = clamp(a.u)
  const top = clamp(a.v)
  const width = clamp(b.u) - left
  const height = clamp(b.v) - top
  return width > 0 && height > 0 && width * height < 0.9
    ? { left, top, width, height }
    : undefined
}

/** A short name of the element for the note's popover, such as button "Sign in". */
export function elementLabel(el: NoteElement) {
  const kind = el.role || el.tag
  return el.name ? `${kind} “${el.name}”` : el.id ? `${kind} #${el.id}` : kind
}

/** The notes that say something, in the order they were placed: their numbers. */
export function writtenNotes(notes: BrowserNote[]) {
  return notes.filter((n) => n.text.trim())
}

/**
 * The message the notes go in: the user's, with or without the screenshot. The notes go in
 * the text rather than drawn into the image: the agent reads text exactly and in full,
 * however long a note is, while the image keeps only the numbers and the elements' boxes,
 * small enough to cover little of the page. Each note names its element in words, with its
 * point as well, so that the text alone says what the note is about, for an agent that can't
 * see the screenshot or isn't sent it.
 */
export function composeNotes(notes: BrowserNote[], screenshot = true) {
  const url = notes.at(0)?.url
  const page = url && url !== "about:blank" ? `, ${url}` : ""
  const one = notes.length === 1
  const named = notes.filter((n) => n.element).length
  const head = [
    `I marked ${one ? "a spot" : `${notes.length} spots`} on your browser's page${page}.`,
  ]
  if (screenshot)
    head.push(
      `The attached screenshot shows ${one ? "it as a numbered circle" : "them as numbered circles"}` +
        (named
          ? `, with a box around the element under ${one ? "it" : "each"}.`
          : ".")
    )
  head.push(
    named === notes.length
      ? `Each note names the element under it, and the brackets give its point in CSS pixels from the top left of the visible page.`
      : named
        ? `A note names the element under it where Studio could tell, and the brackets give each point in CSS pixels from the top left of the visible page.`
        : `The brackets give ${one ? "its" : "each"} point in CSS pixels from the top left of the visible page.`
  )
  if (notes.some((n) => n.element?.field))
    head.push("What form fields hold is left out.")
  const list = notes.map((n, i) => {
    const at = `[${n.x}, ${n.y}]`
    const what = n.element ? `${n.element.description}, at ${at}` : `At ${at}`
    return `${i + 1}. ${what} — ${n.text.trim().replace(/\n/g, "\n   ")}`
  })
  return [head.join(" "), ...list].join("\n\n")
}

/** The colors of a note's circle: the theme's primary, as the markers on the live view. */
function noteColors() {
  const root = getComputedStyle(document.documentElement)
  return {
    fill: root.getPropertyValue("--primary").trim() || "#0f7686",
    text: root.getPropertyValue("--primary-foreground").trim() || "#ffffff",
  }
}

/**
 * The frame on canvas, at its own resolution, with a box around each note's element and its
 * number in a circle on its spot, as a JPEG. A circle's size goes with the frame's width, so
 * that it reads the same on a frame from a HiDPI page.
 */
export function drawNotes(
  canvas: HTMLCanvasElement,
  notes: BrowserNote[]
): Promise<Blob> {
  const out = document.createElement("canvas")
  out.width = canvas.width
  out.height = canvas.height
  const ctx = out.getContext("2d")
  if (!ctx) return Promise.reject(new Error("Could not draw the screenshot"))
  ctx.drawImage(canvas, 0, 0)
  const { fill, text } = noteColors()
  const r = Math.max(10, Math.round(out.width / 90))
  // The boxes go first, so that no box crosses another note's number. Each is a line in the
  // primary colour on a white one, as the circles have a white ring, so that it shows on a
  // page of any colour.
  const w = Math.max(2, Math.round(out.width / 640))
  for (const n of notes) {
    const box = elementBox(n)
    if (!box) continue
    const rect = [
      box.left * out.width + w,
      box.top * out.height + w,
      Math.max(box.width * out.width - 2 * w, 1),
      Math.max(box.height * out.height - 2 * w, 1),
    ] as const
    ctx.lineWidth = 2 * w
    ctx.strokeStyle = "#ffffff"
    ctx.strokeRect(...rect)
    ctx.lineWidth = w
    ctx.strokeStyle = fill
    ctx.strokeRect(...rect)
  }
  notes.forEach((n, i) => {
    const x = n.u * out.width
    const y = n.v * out.height
    ctx.beginPath()
    ctx.arc(x, y, r, 0, 2 * Math.PI)
    ctx.fillStyle = fill
    ctx.fill()
    ctx.lineWidth = Math.max(2, r / 5)
    ctx.strokeStyle = "#ffffff"
    ctx.stroke()
    ctx.fillStyle = text
    ctx.font = `600 ${Math.round(r * 1.1)}px ui-sans-serif, system-ui, sans-serif`
    ctx.textAlign = "center"
    ctx.textBaseline = "middle"
    ctx.fillText(String(i + 1), x, y + r * 0.05)
  })
  return new Promise((resolve, reject) =>
    out.toBlob(
      (blob) =>
        blob
          ? resolve(blob)
          : reject(new Error("Could not encode the screenshot")),
      "image/jpeg",
      0.92
    )
  )
}

/** Where notes can go: a chat of the teammate's, or a new chat in one of its sandboxes. */
export type NoteTarget =
  { kind: "chat"; chat: Chat } | { kind: "new"; sandbox: Sandbox }

/** A target's key for a select. */
export function targetKey(t: NoteTarget) {
  return t.kind === "chat" ? `chat:${t.chat.id}` : `new:${t.sandbox.id}`
}

/**
 * The places a teammate's notes can go, the likeliest first: its open chats, newest first,
 * with those in the sandbox that drove the browser last ahead, then a new chat in each of its
 * sandboxes. Heartbeats and task runs are left out, as the composer does.
 */
export function noteTargets(
  chats: Chat[],
  sandboxes: Sandbox[],
  teammate: string,
  driver?: string
): NoteTarget[] {
  const mine = sandboxes.filter((s) => s.teammateId === teammate)
  const drove = mine.find((s) => s.name === driver)?.id
  const open = chats
    .filter(
      (c) =>
        c.teammateId === teammate &&
        !c.heartbeat &&
        !c.taskId &&
        (c.state === "idle" || c.state === "running")
    )
    .sort(
      (a, b) =>
        Number(b.sandboxId === drove) - Number(a.sandboxId === drove) ||
        b.updatedAt.localeCompare(a.updatedAt)
    )
  const fresh = [...mine].sort(
    (a, b) => Number(b.id === drove) - Number(a.id === drove)
  )
  return [
    ...open.map((chat): NoteTarget => ({ kind: "chat", chat })),
    ...fresh.map((sandbox): NoteTarget => ({ kind: "new", sandbox })),
  ]
}
