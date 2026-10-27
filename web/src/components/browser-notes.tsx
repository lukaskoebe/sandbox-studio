import { useCallback, useRef, useState, type RefObject } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { ChatCircleTextIcon, PaperPlaneTiltIcon } from "@phosphor-icons/react"
import { Button } from "@/components/ui/button"
import {
  Popover,
  PopoverContent,
  PopoverTitle,
  PopoverTrigger,
} from "@/components/ui/popover"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Spinner } from "@/components/ui/spinner"
import { Textarea } from "@/components/ui/textarea"
import { $api, errorMessage, fetchClient } from "@/lib/api/client"
import type { components } from "@/lib/api/schema.gen"
import {
  composeNotes,
  drawNotes,
  elementBox,
  elementLabel,
  noteTargets,
  targetKey,
  writtenNotes,
  type BrowserNote,
  type NoteElement,
  type NoteSpot,
} from "@/lib/browser-notes"
import { chatTitle, readBase64 } from "@/lib/chats"

/** How long the page may take to say what is under a note; then the note keeps its point. */
const lookupTimeout = 3000

/**
 * The notes on a browser window's live view of a session ("" for the profile). One is open at
 * a time; a note left empty when it closes, or when another one opens, goes, so only the new
 * note can be empty and the numbers on the view are the numbers that are sent.
 *
 * A new note opens at once, and Studio asks the page for the element under it meanwhile
 * (GET …/browser/element). A note whose lookup fails or takes too long keeps its point alone.
 */
export function useBrowserNotes(
  env: string,
  teammate: string,
  session: string
) {
  const [notes, setNotes] = useState<BrowserNote[]>([])
  const [open, setOpen] = useState<number>()
  const nextID = useRef(1)
  // The lookups still running, and the elements found, by note: Send waits for the first and
  // reads the second, as the notes it holds are from before the answers came.
  const lookups = useRef(new Map<number, Promise<void>>())
  const found = useRef(new Map<number, NoteElement>())
  const lookUp = (id: number, spot: NoteSpot) => {
    const lookup = fetchClient
      .GET("/api/environments/{env}/teammates/{teammate}/browser/element", {
        params: {
          path: { env, teammate },
          query: { x: spot.x, y: spot.y, session: session || undefined },
        },
        signal: AbortSignal.timeout(lookupTimeout),
      })
      .then(
        ({ data }) => {
          if (!data) return
          found.current.set(id, data)
          setNotes((ns) =>
            ns.map((n) => (n.id === id ? { ...n, element: data } : n))
          )
        },
        () => undefined
      )
      .finally(() => lookups.current.delete(id))
    lookups.current.set(id, lookup)
  }
  const keep = (id?: number) => (n: BrowserNote) =>
    n.id === id || !!n.text.trim()
  const clear = useCallback(() => {
    setNotes([])
    setOpen(undefined)
    found.current.clear()
  }, [])
  return {
    notes,
    open,
    add: (spot: NoteSpot) => {
      const id = nextID.current++
      setNotes((ns) => [...ns.filter(keep()), { ...spot, id, text: "" }])
      setOpen(id)
      // A page that moved on from the frame would name what is there now.
      if (spot.fresh) lookUp(id, spot)
    },
    /** ns with the elements found for them, once the lookups still running are done. */
    withElements: async (ns: BrowserNote[]) => {
      await Promise.all(lookups.current.values())
      return ns.map((n) => ({ ...n, element: found.current.get(n.id) }))
    },
    show: (id: number) => {
      setNotes((ns) => ns.filter(keep(id)))
      setOpen(id)
    },
    close: (id: number) => {
      setNotes((ns) => ns.filter((n) => n.id !== id || n.text.trim()))
      setOpen((o) => (o === id ? undefined : o))
    },
    write: (id: number, text: string) =>
      setNotes((ns) => ns.map((n) => (n.id === id ? { ...n, text } : n))),
    remove: (id: number) => {
      setNotes((ns) => ns.filter((n) => n.id !== id))
      setOpen((o) => (o === id ? undefined : o))
    },
    clear,
  }
}

export type BrowserNotes = ReturnType<typeof useBrowserNotes>

/**
 * The notes' numbered markers over the live view, each on its spot as the view resizes. A
 * marker opens its note, to edit or remove it.
 */
export function NoteMarkers({
  notes,
  name,
}: {
  notes: BrowserNotes
  /** The teammate's name, for the placeholder. */
  name: string
}) {
  return (
    <div className="pointer-events-none absolute inset-0">
      {notes.notes.map((n, i) => (
        <NoteMarker
          key={n.id}
          note={n}
          number={i + 1}
          name={name}
          open={notes.open === n.id}
          onOpen={() => notes.show(n.id)}
          onClose={() => notes.close(n.id)}
          onWrite={(text) => notes.write(n.id, text)}
          onRemove={() => notes.remove(n.id)}
        />
      ))}
    </div>
  )
}

function NoteMarker({
  note,
  number,
  name,
  open,
  onOpen,
  onClose,
  onWrite,
  onRemove,
}: {
  note: BrowserNote
  number: number
  name: string
  open: boolean
  onOpen: () => void
  onClose: () => void
  onWrite: (text: string) => void
  onRemove: () => void
}) {
  const input = useRef<HTMLTextAreaElement>(null)
  const [hovered, setHovered] = useState(false)
  const text = note.text.trim()
  const box = elementBox(note)
  return (
    <Popover open={open} onOpenChange={(next) => (next ? onOpen() : onClose())}>
      {box && (open || hovered) && (
        // The element the note is on, while the note is open or the pointer is on its marker.
        <div
          data-testid="note-element"
          className="pointer-events-none absolute rounded-[2px] bg-primary/10 ring-2 ring-primary"
          style={{
            left: `${box.left * 100}%`,
            top: `${box.top * 100}%`,
            width: `${box.width * 100}%`,
            height: `${box.height * 100}%`,
          }}
        />
      )}
      <PopoverTrigger
        aria-label={text ? `Note ${number}: ${text}` : `Note ${number}`}
        title={text || undefined}
        style={{ left: `${note.u * 100}%`, top: `${note.v * 100}%` }}
        onPointerEnter={() => setHovered(true)}
        onPointerLeave={() => setHovered(false)}
        className="pointer-events-auto absolute flex size-6 -translate-x-1/2 -translate-y-1/2 items-center justify-center rounded-full bg-primary text-caption font-semibold text-primary-foreground shadow-md ring-2 ring-background transition-transform outline-none hover:scale-110 focus-visible:ring-ring data-popup-open:scale-110"
        onContextMenu={(e) => {
          // A right-click on a marker opens it, rather than pinning a note on top of it.
          e.preventDefault()
          e.stopPropagation()
          onOpen()
        }}
      >
        {number}
      </PopoverTrigger>
      <PopoverContent
        side="right"
        sideOffset={10}
        initialFocus={input}
        className="grid w-72 gap-2"
        // React passes events up through the portal: a right-click in the note is the
        // textarea's own, not one on the live view.
        onContextMenu={(e) => e.stopPropagation()}
      >
        <div className="grid gap-0.5">
          <PopoverTitle>Note {number}</PopoverTitle>
          {note.element && (
            <p
              className="truncate text-muted-foreground"
              title={note.element.description}
            >
              On {elementLabel(note.element)}
            </p>
          )}
        </div>
        <Textarea
          ref={input}
          aria-label={`Note ${number}`}
          rows={3}
          maxLength={2000}
          value={note.text}
          placeholder={`What should ${name} know about this spot?`}
          onChange={(e) => onWrite(e.target.value)}
          onKeyDown={(e) => {
            // Enter keeps the note, as it sends in the composer; Shift+Enter starts a line.
            if (
              e.key === "Enter" &&
              !e.shiftKey &&
              !e.nativeEvent.isComposing
            ) {
              e.preventDefault()
              onClose()
            }
          }}
        />
        <div className="flex items-center gap-1.5">
          <span className="min-w-0 flex-1 text-muted-foreground">
            Shift+Enter for a new line
          </span>
          <Button size="sm" variant="ghost" onClick={onRemove}>
            Remove
          </Button>
          <Button size="sm" onClick={onClose}>
            Done
          </Button>
        </div>
      </PopoverContent>
    </Popover>
  )
}

type PromptRequest = components["schemas"]["PromptRequest"]

/** Whether a send was refused for its screenshot alone, by an agent that can't take images. */
function takesNoImages(error: unknown) {
  const errors =
    error && typeof error === "object"
      ? (error as { errors?: { location?: string }[] }).errors
      : undefined
  return !!errors?.some((e) => e.location === "body.attachments")
}

/**
 * The bar of a live view with notes: how many, where they go, and Send, which posts the frame
 * with the notes' numbers and their elements' boxes on it and the notes as the user's message.
 */
export function NotesBar({
  env,
  teammate,
  name,
  driver,
  notes,
  canvas,
}: {
  env: string
  teammate: string
  name: string
  /** The sandbox that drove the browser last: its chat is the likeliest place. */
  driver?: string
  notes: BrowserNotes
  /** The live view's canvas, with the frame the notes are on. */
  canvas: RefObject<HTMLCanvasElement | null>
}) {
  const queryClient = useQueryClient()
  const chats = $api.useQuery("get", "/api/environments/{env}/chats", {
    params: { path: { env } },
  })
  const sandboxes = $api.useQuery("get", "/api/environments/{env}/sandboxes", {
    params: { path: { env } },
  })
  const targets = noteTargets(
    chats.data ?? [],
    sandboxes.data ?? [],
    teammate,
    driver
  )
  const [picked, setPicked] = useState<string>()
  const target = targets.find((t) => targetKey(t) === picked) ?? targets.at(0)
  const [sending, setSending] = useState(false)
  const written = writtenNotes(notes.notes)

  const send = async () => {
    const frame = canvas.current
    if (!target || !frame || !written.length) return
    setSending(true)
    try {
      const sent = await notes.withElements(written)
      const blob = await drawNotes(frame, sent)
      const file = `browser-notes-${new Date().toISOString().replace(/[:.]/g, "-")}.jpg`
      const data = await readBase64(new File([blob], file, { type: blob.type }))
      const post = (body: PromptRequest) =>
        target.kind === "chat"
          ? fetchClient.POST("/api/environments/{env}/chats/{chat}/queue", {
              params: { path: { env, chat: target.chat.id } },
              body,
            })
          : fetchClient.POST("/api/environments/{env}/sandboxes/{id}/chats", {
              params: { path: { env, id: target.sandbox.id } },
              body,
            })
      let { error } = await post({
        text: composeNotes(sent),
        attachments: [{ name: file, mimeType: blob.type, data }],
      })
      // An agent that can't take images gets the notes as text alone: each names its
      // element and point, so they read without the screenshot.
      const textOnly = takesNoImages(error)
      if (textOnly)
        ({ error } = await post({ text: composeNotes(sent, false) }))
      if (error) throw error
      const where =
        target.kind === "chat"
          ? target.chat.state === "running"
            ? `Notes queued for ${name}: they go once the current turn ends`
            : `Notes sent to ${chatTitle(target.chat)}`
          : `Notes sent to a new chat in ${target.sandbox.name}`
      if (textOnly)
        toast.warning(where, {
          description: `${name}'s agent can't take images, so the screenshot stayed behind. Each note names the element it is on and its point on the page.`,
        })
      else toast.success(where)
      for (const path of [
        "/api/environments/{env}/chats",
        "/api/environments/{env}/sandboxes/{id}/chats",
        "/api/environments/{env}/teammates/{id}/timeline",
      ])
        void queryClient.invalidateQueries({ queryKey: ["get", path] })
      notes.clear()
    } catch (err) {
      toast.error("Could not send the notes", {
        description: errorMessage(err),
      })
    } finally {
      setSending(false)
    }
  }

  const options = Object.fromEntries(
    targets.map((t) => [
      targetKey(t),
      t.kind === "chat"
        ? `${chatTitle(t.chat)}${t.chat.state === "running" ? " (after its turn)" : ""}`
        : `A new chat in ${t.sandbox.name}`,
    ])
  )
  const count = written.length
  return (
    <div className="flex min-h-9 shrink-0 flex-wrap items-center gap-x-2 gap-y-1 border-b bg-muted/40 px-3 py-1.5 text-xs">
      <ChatCircleTextIcon
        className="size-3.5 shrink-0 text-primary"
        weight="fill"
      />
      <span
        className="min-w-0 flex-1"
        title="The view holds still until you send or clear the notes."
      >
        <span className="font-medium">
          {count === 0 ? "New note" : count === 1 ? "1 note" : `${count} notes`}
        </span>
      </span>
      {targets.length > 0 ? (
        <Select
          value={target ? targetKey(target) : ""}
          items={options}
          onValueChange={(v) => v && setPicked(v)}
        >
          <SelectTrigger
            size="sm"
            aria-label="Send the notes to"
            className="max-w-40 min-w-0"
          >
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {Object.entries(options).map(([v, label]) => (
              <SelectItem key={v} value={v}>
                {label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      ) : (
        !chats.isPending &&
        !sandboxes.isPending && (
          <span className="text-muted-foreground">
            {name} has no sandbox to chat in yet.
          </span>
        )
      )}
      <Button
        size="xs"
        disabled={!target || count === 0 || sending}
        onClick={() => void send()}
      >
        {sending ? <Spinner /> : <PaperPlaneTiltIcon />}
        Send to chat
      </Button>
      <Button
        size="xs"
        variant="outline"
        disabled={sending}
        onClick={notes.clear}
      >
        Clear
      </Button>
    </div>
  )
}
