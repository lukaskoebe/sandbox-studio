import {
  useEffect,
  useId,
  useRef,
  useState,
  type ClipboardEvent,
  type KeyboardEvent,
  type ReactNode,
} from "react"
import {
  ArrowUpIcon,
  FileIcon,
  ImageIcon,
  PaperclipIcon,
  StopIcon,
  XIcon,
} from "@phosphor-icons/react"
import {
  Attachment,
  AttachmentAction,
  AttachmentActions,
  AttachmentContent,
  AttachmentDescription,
  AttachmentGroup,
  AttachmentMedia,
  AttachmentTitle,
} from "@/components/ui/attachment"
import {
  MentionBackdrop,
  MentionPicker,
  useMentionIndex,
  usePicks,
} from "@/components/chat/mention-picker"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/spinner"
import { Textarea } from "@/components/ui/textarea"
import type { components } from "@/lib/api/schema.gen"
import { maxAttachmentBytes, readBase64 } from "@/lib/chats"
import {
  activeQuery,
  keptMentions,
  listenForMentions,
  mentionText,
  type Candidate,
  type Mention,
} from "@/lib/mentions"
import { cn, formatBytes } from "@/lib/utils"

export type Prompt = components["schemas"]["PromptRequest"]

/** At most this many files per message. */
const maxFiles = 20

/**
 * Where the person writes to the agent: Enter sends, Shift+Enter starts a new line.
 * Files come from the paperclip or a paste; together they stay under the 10 MiB the server
 * takes. While the agent works, Stop joins the send button, and what is sent goes into the
 * running turn; with Alt it waits for the turn to end (onSend's after). With a sandbox to
 * mention files of, @ opens a picker over its workspace, and Files can mention a file here
 * too.
 */
export function Composer({
  onSend,
  onStop,
  running = false,
  disabled,
  placeholder = "Ask for something…",
  autoFocus,
  error,
  toolbar,
  hint,
  attachments: canAttach = true,
  insert,
  mentionsIn,
  className,
}: {
  /**
   * Sends the prompt; resolves true when it went through, so the draft clears. While running,
   * after asks for it to wait for the turn to end rather than go into it.
   */
  onSend: (p: Prompt, opts: { after: boolean }) => Promise<boolean>
  onStop?: () => void
  running?: boolean
  /** Why nothing can be sent right now, shown instead of the box. */
  disabled?: string
  placeholder?: string
  autoFocus?: boolean
  error?: string
  /** A row above the text, inside the box: the teammate page's modes and sandbox. */
  toolbar?: ReactNode
  /** A short note beside the buttons. */
  hint?: ReactNode
  /** Whether files can be attached; without, the paperclip is gone and pasted files ignored. */
  attachments?: boolean
  /** Text to put in the draft, such as a queued message taken back to edit; once per id. */
  insert?: { id: string; text: string }
  /** The sandbox whose workspace files @ mentions. */
  mentionsIn?: { env: string; sandbox: string }
  className?: string
}) {
  const [text, setText] = useState("")
  const [inserted, setInserted] = useState<string>()
  if (insert && insert.id !== inserted) {
    setInserted(insert.id)
    setText((t) => (t.trim() ? `${t}\n\n${insert.text}` : insert.text))
  }
  const [files, setFiles] = useState<File[]>([])
  const [problem, setProblem] = useState<string>()
  const [sending, setSending] = useState(false)
  const [stopping, setStopping] = useState(false)
  const input = useRef<HTMLInputElement>(null)
  const area = useRef<HTMLTextAreaElement>(null)
  const boxRef = useRef<HTMLDivElement>(null)
  const backdrop = useRef<HTMLDivElement>(null)
  const size = files.reduce((n, f) => n + f.size, 0)

  // Mentions: what the picker added, kept while the text still has them.
  const [mentions, setMentions] = useState<Mention[]>([])
  const [caret, setCaret] = useState(0)
  // The @ that Esc closed the picker at, so it stays closed until another.
  const [dismissed, setDismissed] = useState<number>()
  const [active, setActive] = useState(0)
  const pickerId = useId()
  const at = mentionsIn ? activeQuery(text, caret) : undefined
  const picking = !!at && at.at !== dismissed
  const { index, all } = useMentionIndex(
    mentionsIn?.env ?? "",
    mentionsIn?.sandbox ?? "",
    picking
  )
  const picks = usePicks(all, at?.query ?? "")
  const [shownQuery, setShownQuery] = useState<string>()
  if (picking && at.query !== shownQuery) {
    setShownQuery(at.query)
    setActive(0)
  }

  /** Replaces the text from..to and puts the caret after the replacement. */
  const splice = (from: number, to: number, with_: string) => {
    const next = text.slice(0, from) + with_ + text.slice(to)
    const pos = from + with_.length
    setText(next)
    setCaret(pos)
    requestAnimationFrame(() => {
      area.current?.focus()
      area.current?.setSelectionRange(pos, pos)
    })
  }
  const pick = (c: Candidate) => {
    if (!at) return
    const end = at.at + 1 + at.query.length
    // A folder narrows the picker to what is in it.
    if (c.folder) return splice(at.at, end, `@${c.path}`)
    const m: Mention = { path: c.path }
    if (picks.lines.start) m.start = picks.lines.start
    if (picks.lines.end) m.end = picks.lines.end
    setMentions((list) => [...list, m])
    const after = text[end] === " " ? "" : " "
    splice(at.at, end, mentionText(m) + after)
  }

  // Files mentions a file here: at the caret, with a space before it.
  const latest = useRef({ text, caret })
  latest.current = { text, caret }
  const sandbox = mentionsIn?.sandbox
  useEffect(() => {
    if (!sandbox) return
    return listenForMentions(sandbox, (m) => {
      const { text: t, caret: c } = latest.current
      const before = c > 0 && !/\s/.test(t[c - 1]) ? " " : ""
      const added = `${before}${mentionText(m)} `
      const next = t.slice(0, c) + added + t.slice(c)
      const pos = c + added.length
      setMentions((list) => [...list, m])
      setText(next)
      setCaret(pos)
      requestAnimationFrame(() => {
        area.current?.focus()
        area.current?.setSelectionRange(pos, pos)
      })
    })
  }, [sandbox])

  const add = (list: File[]) => {
    if (list.length === 0) return
    const next = [...files, ...list]
    if (next.length > maxFiles) {
      setProblem(`You can attach up to ${maxFiles} files to one message.`)
      return
    }
    const total = next.reduce((n, f) => n + f.size, 0)
    if (total > maxAttachmentBytes) {
      setProblem(
        `Attachments can add up to ${formatBytes(maxAttachmentBytes)} per message; these would be ${formatBytes(total)}.`
      )
      return
    }
    setProblem(undefined)
    setFiles(next)
  }

  const send = async (after = false) => {
    if (sending || disabled) return
    if (!text.trim() && files.length === 0) return
    setSending(true)
    const draft = { text, files, mentions }
    // The message leaves the box at once, and comes back if it didn't go, ahead of anything
    // typed meanwhile.
    const restore = () => {
      setText((t) => (t.trim() ? `${draft.text}\n\n${t}` : draft.text))
      setFiles((f) => [...draft.files, ...f])
      setMentions((m) => [...draft.mentions, ...m])
    }
    try {
      const attachments = await Promise.all(
        files.map(async (f) => ({
          name: f.name,
          mimeType: f.type || undefined,
          data: await readBase64(f),
        }))
      )
      const sent = keptMentions(text, mentions)
      setText("")
      setFiles([])
      setMentions([])
      setProblem(undefined)
      const ok = await onSend(
        {
          text: text.trim() || undefined,
          attachments: attachments.length > 0 ? attachments : undefined,
          mentions: sent.length > 0 ? sent : undefined,
        },
        { after: running && after }
      )
      if (!ok) restore()
    } catch (err) {
      restore()
      setProblem(err instanceof Error ? err.message : String(err))
    } finally {
      setSending(false)
    }
  }

  const stop = () => {
    if (!onStop) return
    setStopping(true)
    onStop()
    // The stream reports when the turn ends; stop showing progress after a moment either way.
    setTimeout(() => setStopping(false), 3000)
  }

  const onKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (picking && !e.nativeEvent.isComposing) {
      const n = picks.shown.length
      const key = e.key
      if ((key === "ArrowDown" || key === "ArrowUp") && n > 0) {
        e.preventDefault()
        setActive((i) => (i + (key === "ArrowDown" ? 1 : n - 1)) % n)
        return
      }
      if ((key === "Enter" || key === "Tab") && !e.shiftKey && n > 0) {
        e.preventDefault()
        pick(picks.shown[Math.min(active, n - 1)])
        return
      }
      if (key === "Escape") {
        e.preventDefault()
        e.stopPropagation()
        setDismissed(at.at)
        return
      }
    }
    if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
      e.preventDefault()
      void send(e.altKey)
    }
  }
  const onPaste = (e: ClipboardEvent<HTMLTextAreaElement>) => {
    const pasted = [...e.clipboardData.files]
    if (pasted.length === 0 || !canAttach) return
    e.preventDefault()
    add(pasted)
  }

  if (disabled) {
    return (
      <p className="rounded-lg border border-dashed px-3 py-2.5 text-center text-xs text-muted-foreground">
        {disabled}
      </p>
    )
  }

  const shown = problem ?? error
  return (
    <div className="grid grid-cols-1 gap-1.5">
      <div
        ref={boxRef}
        className={cn(
          "grid grid-cols-1 gap-2 rounded-lg border bg-background p-2 shadow-xs focus-within:border-ring focus-within:ring-2 focus-within:ring-ring/30",
          shown && "border-destructive/60",
          className
        )}
      >
        {toolbar}
        {files.length > 0 && (
          <AttachmentGroup>
            {files.map((f, i) => (
              <Attachment key={`${f.name}-${i}`} size="sm">
                <AttachmentMedia>
                  {f.type.startsWith("image/") ? <ImageIcon /> : <FileIcon />}
                </AttachmentMedia>
                <AttachmentContent>
                  <AttachmentTitle>{f.name}</AttachmentTitle>
                  <AttachmentDescription>
                    {formatBytes(f.size)}
                  </AttachmentDescription>
                </AttachmentContent>
                <AttachmentActions>
                  <AttachmentAction
                    aria-label={`Remove ${f.name}`}
                    onClick={() => {
                      setFiles(files.filter((_, j) => j !== i))
                      setProblem(undefined)
                    }}
                  >
                    <XIcon />
                  </AttachmentAction>
                </AttachmentActions>
              </Attachment>
            ))}
          </AttachmentGroup>
        )}
        {picking && (
          <MentionPicker
            anchor={boxRef}
            id={pickerId}
            query={at.query}
            picks={picks.shown}
            active={active}
            total={index.data?.paths?.length ?? 0}
            loading={index.isPending}
            error={index.error}
            onPick={pick}
            onHover={setActive}
          />
        )}
        <div className="relative grid">
          <MentionBackdrop
            ref={backdrop}
            text={text}
            mentions={mentions}
            className="p-1 text-sm md:text-xs/relaxed"
          />
          <Textarea
            ref={area}
            value={text}
            onChange={(e) => {
              setText(e.target.value)
              setCaret(e.target.selectionStart)
            }}
            onSelect={(e) => setCaret(e.currentTarget.selectionStart)}
            onScroll={(e) => {
              if (backdrop.current)
                backdrop.current.scrollTop = e.currentTarget.scrollTop
            }}
            onBlur={() => setDismissed(at?.at)}
            onFocus={() => setDismissed(undefined)}
            onKeyDown={onKeyDown}
            onPaste={onPaste}
            placeholder={placeholder}
            autoFocus={autoFocus}
            aria-label="Message"
            aria-controls={picking ? pickerId : undefined}
            aria-activedescendant={
              picking && picks.shown.length > 0
                ? `${pickerId}-${Math.min(active, picks.shown.length - 1)}`
                : undefined
            }
            aria-autocomplete={mentionsIn ? "list" : undefined}
            className="relative max-h-60 min-h-12 resize-none border-0 bg-transparent p-1 shadow-none focus-visible:ring-0 dark:bg-transparent"
          />
        </div>
        <div className="flex items-center gap-1.5">
          <input
            ref={input}
            type="file"
            multiple
            hidden
            onChange={(e) => {
              add([...(e.target.files ?? [])])
              e.target.value = ""
            }}
          />
          {canAttach && (
            <Button
              variant="ghost"
              size="icon-sm"
              aria-label="Attach files"
              title="Attach files"
              onClick={() => input.current?.click()}
            >
              <PaperclipIcon />
            </Button>
          )}
          {hint && (
            <span className="min-w-0 truncate px-1 text-xs text-muted-foreground">
              {hint}
            </span>
          )}
          {size > 0 && (
            <span className="text-xs text-muted-foreground tabular-nums">
              {formatBytes(size)} of {formatBytes(maxAttachmentBytes)}
            </span>
          )}
          {running && (
            <Button
              size="sm"
              variant="outline"
              className="ml-auto"
              disabled={stopping}
              onClick={stop}
            >
              {stopping ? <Spinner /> : <StopIcon weight="fill" />}
              Stop
            </Button>
          )}
          <Button
            size="icon-sm"
            className={cn(!running && "ml-auto")}
            aria-label={running ? "Send now" : "Send"}
            title={
              running
                ? "Send now (Enter): it goes into this turn. With Alt, it waits for the turn to end"
                : "Send (Enter)"
            }
            disabled={sending || (!text.trim() && files.length === 0)}
            onClick={(e) => void send(e.altKey)}
          >
            {sending ? <Spinner /> : <ArrowUpIcon />}
          </Button>
        </div>
      </div>
      {shown && (
        <p role="alert" className="px-1 text-xs text-destructive">
          {shown}
        </p>
      )}
    </div>
  )
}
