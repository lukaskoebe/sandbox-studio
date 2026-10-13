import {
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
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/spinner"
import { Textarea } from "@/components/ui/textarea"
import type { components } from "@/lib/api/schema.gen"
import { maxAttachmentBytes, readBase64 } from "@/lib/chats"
import { cn, formatBytes } from "@/lib/utils"

export type Prompt = components["schemas"]["PromptRequest"]

/** At most this many files per message. */
const maxFiles = 20

/**
 * Where the person writes to the agent: Enter sends, Shift+Enter starts a new line.
 * Files come from the paperclip or a paste; together they stay under the 10 MiB the server
 * takes. While the agent works the send button becomes Stop.
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
  className,
}: {
  /** Sends the prompt; resolves true when it went through, so the draft clears. */
  onSend: (p: Prompt) => Promise<boolean>
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
  className?: string
}) {
  const [text, setText] = useState("")
  const [files, setFiles] = useState<File[]>([])
  const [problem, setProblem] = useState<string>()
  const [sending, setSending] = useState(false)
  const [stopping, setStopping] = useState(false)
  const input = useRef<HTMLInputElement>(null)
  const size = files.reduce((n, f) => n + f.size, 0)

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

  const send = async () => {
    if (sending || running || disabled) return
    if (!text.trim() && files.length === 0) return
    setSending(true)
    try {
      const attachments = await Promise.all(
        files.map(async (f) => ({
          name: f.name,
          mimeType: f.type || undefined,
          data: await readBase64(f),
        }))
      )
      const ok = await onSend({
        text: text.trim() || undefined,
        attachments: attachments.length > 0 ? attachments : undefined,
      })
      if (ok) {
        setText("")
        setFiles([])
        setProblem(undefined)
      }
    } catch (err) {
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
    if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
      e.preventDefault()
      void send()
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
        <Textarea
          value={text}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={onKeyDown}
          onPaste={onPaste}
          placeholder={placeholder}
          autoFocus={autoFocus}
          aria-label="Message"
          className="max-h-60 min-h-12 resize-none border-0 bg-transparent p-1 shadow-none focus-visible:ring-0 dark:bg-transparent"
        />
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
          {running ? (
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
          ) : (
            <Button
              size="icon-sm"
              className="ml-auto"
              aria-label="Send"
              title="Send (Enter)"
              disabled={sending || (!text.trim() && files.length === 0)}
              onClick={() => void send()}
            >
              {sending ? <Spinner /> : <ArrowUpIcon />}
            </Button>
          )}
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
