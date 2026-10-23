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
import {
  composeNotes,
  drawNotes,
  noteTargets,
  targetKey,
  writtenNotes,
  type BrowserNote,
  type NoteSpot,
} from "@/lib/browser-notes"
import { chatTitle, readBase64 } from "@/lib/chats"
import { cn } from "@/lib/utils"

/**
 * The notes on a browser window's live view. One is open at a time; a note left empty when
 * it closes, or when another one opens, goes, so only the new note can be empty and the
 * numbers on the view are the numbers that are sent.
 */
export function useBrowserNotes() {
  const [notes, setNotes] = useState<BrowserNote[]>([])
  const [open, setOpen] = useState<number>()
  const nextID = useRef(1)
  const keep = (id?: number) => (n: BrowserNote) =>
    n.id === id || !!n.text.trim()
  const clear = useCallback(() => {
    setNotes([])
    setOpen(undefined)
  }, [])
  return {
    notes,
    open,
    add: (spot: NoteSpot) => {
      const id = nextID.current++
      setNotes((ns) => [...ns.filter(keep()), { ...spot, id, text: "" }])
      setOpen(id)
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
  const text = note.text.trim()
  return (
    <Popover open={open} onOpenChange={(next) => (next ? onOpen() : onClose())}>
      <PopoverTrigger
        aria-label={text ? `Note ${number}: ${text}` : `Note ${number}`}
        title={text || undefined}
        style={{ left: `${note.u * 100}%`, top: `${note.v * 100}%` }}
        className="pointer-events-auto absolute flex size-6 -translate-x-1/2 -translate-y-1/2 items-center justify-center rounded-full bg-primary text-[0.6875rem] font-semibold text-primary-foreground shadow-md ring-2 ring-background transition-transform outline-none hover:scale-110 focus-visible:ring-ring data-popup-open:scale-110"
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
        <PopoverTitle>Note {number}</PopoverTitle>
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

/**
 * The bar of a live view with notes: how many, where they go, and Send, which posts the frame
 * with the notes' numbers on it and the notes as the user's message.
 */
export function NotesBar({
  env,
  teammate,
  name,
  driver,
  notes,
  canvas,
  panel,
}: {
  env: string
  teammate: string
  name: string
  /** The sandbox that drove the browser last: its chat is the likeliest place. */
  driver?: string
  notes: BrowserNotes
  /** The live view's canvas, with the frame the notes are on. */
  canvas: RefObject<HTMLCanvasElement | null>
  panel: boolean
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
      const blob = await drawNotes(frame, written)
      const file = `browser-notes-${new Date().toISOString().replace(/[:.]/g, "-")}.jpg`
      const data = await readBase64(new File([blob], file, { type: blob.type }))
      const body = {
        text: composeNotes(written),
        attachments: [{ name: file, mimeType: blob.type, data }],
      }
      const { error } =
        target.kind === "chat"
          ? await fetchClient.POST(
              "/api/environments/{env}/chats/{chat}/queue",
              { params: { path: { env, chat: target.chat.id } }, body }
            )
          : await fetchClient.POST(
              "/api/environments/{env}/sandboxes/{id}/chats",
              { params: { path: { env, id: target.sandbox.id } }, body }
            )
      if (error) throw error
      toast.success(
        target.kind === "chat"
          ? target.chat.state === "running"
            ? `Notes queued for ${name}: they go once the current turn ends`
            : `Notes sent to ${chatTitle(target.chat)}`
          : `Notes sent to a new chat in ${target.sandbox.name}`
      )
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
        {/* A side panel has no room to say why the view stopped moving: the title says it. */}
        {!panel && (
          <span className="text-muted-foreground">
            {" "}
            · the view holds still until you send or clear
          </span>
        )}
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
            className={cn("min-w-0", panel ? "max-w-40" : "max-w-64")}
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
