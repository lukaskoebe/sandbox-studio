import {
  createContext,
  useContext,
  useLayoutEffect,
  useMemo,
  useState,
  type ReactNode,
  type RefObject,
} from "react"
import { createPortal } from "react-dom"
import { Link } from "@tanstack/react-router"
import { FileIcon, FolderIcon } from "@phosphor-icons/react"
import { PreviewSandbox } from "@/components/chat/markdown"
import { Spinner } from "@/components/ui/spinner"
import { $api, errorMessage } from "@/lib/api/client"
import {
  candidates,
  mentionLabel,
  mentionRanges,
  rank,
  splitLines,
  type Candidate,
  type Mention,
} from "@/lib/mentions"
import { cn } from "@/lib/utils"

/** The workspace's files a message can mention, fetched once the picker opens. */
export function useMentionIndex(
  env: string,
  sandbox: string,
  enabled: boolean
) {
  const index = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes/{id}/files/index",
    { params: { path: { env, id: sandbox } } },
    { enabled: enabled && !!sandbox, staleTime: 30_000, retry: false }
  )
  const all = useMemo(
    () =>
      index.data
        ? candidates(index.data.paths ?? [], index.data.changed ?? [])
        : undefined,
    [index.data]
  )
  return { index, all }
}

/** The ranked files for what follows the @, and the lines it asks for. */
export function usePicks(all: Candidate[] | undefined, query: string) {
  return useMemo(() => {
    const lines = splitLines(query)
    const ranked = all ? rank(all, lines.query) : { total: 0, shown: [] }
    return { ...ranked, lines }
  }, [all, query])
}

/**
 * The list above the composer while an @ is typed, as mock J draws it: the best matches, a
 * file's folder beside its name, and the keys. It sits on the page over the composer's box,
 * as the dock around the composer clips what leaves it.
 */
export function MentionPicker({
  anchor,
  id,
  query,
  picks,
  active,
  total,
  loading,
  error,
  onPick,
  onHover,
}: {
  anchor: RefObject<HTMLElement | null>
  id: string
  query: string
  picks: Candidate[]
  active: number
  /** Files in the index. */
  total: number
  loading: boolean
  error?: unknown
  onPick: (c: Candidate) => void
  onHover: (i: number) => void
}) {
  const [at, setAt] = useState<{
    left: number
    width: number
    bottom: number
  }>()
  useLayoutEffect(() => {
    const place = () => {
      const r = anchor.current?.getBoundingClientRect()
      if (r)
        setAt({
          left: r.left + 8,
          width: r.width - 16,
          bottom: window.innerHeight - r.top + 6,
        })
    }
    place()
    // The box grows with the text, and the page moves under it.
    const seen = new ResizeObserver(place)
    if (anchor.current) seen.observe(anchor.current)
    window.addEventListener("resize", place)
    return () => {
      seen.disconnect()
      window.removeEventListener("resize", place)
    }
  }, [anchor])
  if (!at) return null
  return createPortal(
    <div
      style={at}
      className="fixed z-50 grid overflow-hidden rounded-lg border bg-popover text-popover-foreground shadow-md"
    >
      <div className="flex items-center gap-2 border-b px-2.5 py-1.5 text-caption text-muted-foreground">
        <span className="font-medium text-foreground">Files in /workspace</span>
        <span className="ml-auto truncate tabular-nums">
          {query ? `“${query}” · ` : ""}
          {loading ? "" : `${picks.length} of ${total.toLocaleString()}`}
        </span>
      </div>
      {loading ? (
        <div className="flex items-center gap-2 px-2.5 py-2 text-caption text-muted-foreground">
          <Spinner /> Listing the files…
        </div>
      ) : error ? (
        <p className="px-2.5 py-2 text-caption text-destructive">
          Can't list the files: {errorMessage(error)}
        </p>
      ) : picks.length === 0 ? (
        <p className="px-2.5 py-2 text-caption text-muted-foreground">
          No file matches.
        </p>
      ) : (
        <div
          id={id}
          role="listbox"
          aria-label="Files to mention"
          className="grid py-1"
        >
          {picks.map((c, i) => (
            <CandidateRow
              key={c.path}
              id={`${id}-${i}`}
              candidate={c}
              active={i === active}
              onPick={() => onPick(c)}
              onHover={() => onHover(i)}
            />
          ))}
        </div>
      )}
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1 border-t px-2.5 py-1.5 text-meta text-muted-foreground">
        <span>
          <Kbd>↑↓</Kbd> choose
        </span>
        <span>
          <Kbd>Enter</Kbd> add
        </span>
        <span>
          <Kbd>Tab</Kbd> into a folder
        </span>
        <span>
          <Kbd>:</Kbd> lines, as coupon.ts:20-28
        </span>
        <span className="ml-auto">
          <Kbd>Esc</Kbd>
        </span>
      </div>
    </div>,
    document.body
  )
}

/** A file or folder in a list to pick from: its name, its folder, and whether it changed. */
export function CandidateRow({
  id,
  candidate: c,
  active,
  onPick,
  onHover,
}: {
  id: string
  candidate: Candidate
  active: boolean
  onPick: () => void
  onHover: () => void
}) {
  return (
    <div
      id={id}
      role="option"
      aria-selected={active}
      onMouseDown={(e) => {
        // The text box keeps the focus and the caret.
        e.preventDefault()
        onPick()
      }}
      onMouseMove={onHover}
      className={cn(
        "flex h-7 cursor-default items-center gap-2 px-2.5 font-mono text-caption",
        active && "bg-muted shadow-[inset_2px_0_0_var(--primary)]"
      )}
    >
      {c.folder ? (
        <FolderIcon className="size-3.5 shrink-0 text-muted-foreground" />
      ) : (
        <FileIcon className="size-3.5 shrink-0 text-muted-foreground" />
      )}
      <span className="shrink-0">
        <Highlight text={c.folder ? `${c.name}/` : c.name} match={c.match} />
      </span>
      <span className="min-w-0 truncate text-muted-foreground">{c.dir}</span>
      {c.changed && (
        <span className="ml-auto shrink-0 rounded-xs border border-amber-500/40 px-1 text-meta text-amber-600 dark:text-amber-400">
          changed
        </span>
      )}
      {c.folder && (
        <span className="ml-auto shrink-0 rounded-xs border px-1 text-meta text-muted-foreground">
          folder · {c.files}
        </span>
      )}
    </div>
  )
}

function Kbd({ children }: { children: ReactNode }) {
  return (
    <kbd className="rounded-xs border px-1 font-mono text-meta">{children}</kbd>
  )
}

function Highlight({
  text,
  match,
}: {
  text: string
  match?: [number, number]
}) {
  if (!match) return text
  return (
    <>
      {text.slice(0, match[0])}
      <b className="font-semibold text-primary">
        {text.slice(match[0], match[1])}
      </b>
      {text.slice(match[1])}
    </>
  )
}

/**
 * Behind the composer's text, the mentions it has as chips: the textarea's own text stays on
 * top, so the caret and selection work as ever.
 */
export function MentionBackdrop({
  text,
  mentions,
  className,
  ref,
}: {
  text: string
  mentions: Mention[]
  className?: string
  ref?: React.Ref<HTMLDivElement>
}) {
  const ranges = mentionRanges(text, mentions)
  if (ranges.length === 0) return null
  const parts: ReactNode[] = []
  let at = 0
  for (const [start, end] of ranges) {
    parts.push(text.slice(at, start))
    parts.push(
      <mark
        key={start}
        className="rounded-xs bg-(--acc-chat)/15 text-transparent ring-1 ring-(--acc-chat)/45"
      >
        {text.slice(start, end)}
      </mark>
    )
    at = end
  }
  // A final newline needs a character after it to take up its line.
  parts.push(text.slice(at) + "​")
  return (
    <div
      ref={ref}
      aria-hidden
      className={cn(
        "pointer-events-none absolute inset-0 overflow-hidden break-words whitespace-pre-wrap text-transparent",
        className
      )}
    >
      {parts}
    </div>
  )
}

/**
 * Opens a mentioned file where the chat is: the teammate page's computer shows it in Files.
 * Without it, a mention links to the sandbox's page.
 */
export const OpenWorkspaceFile = createContext<((path: string) => void) | null>(
  null
)

/** A sent message's text with its mentions as links to the files. */
export function MentionedText({
  text,
  mentions,
}: {
  text: string
  mentions?: Mention[]
}) {
  const ranges = mentions?.length ? mentionRanges(text, mentions) : []
  if (ranges.length === 0) return text
  const parts: ReactNode[] = []
  let at = 0
  for (const [start, end, m] of ranges) {
    parts.push(text.slice(at, start))
    parts.push(<MentionChip key={start} mention={m} />)
    at = end
  }
  parts.push(text.slice(at))
  return parts
}

function MentionChip({ mention }: { mention: Mention }) {
  const open = useContext(OpenWorkspaceFile)
  const sandbox = useContext(PreviewSandbox)
  const name = mentionLabel({
    ...mention,
    path: mention.path.split("/").at(-1) ?? mention.path,
  })
  const title = `/workspace/${mention.path}${
    mention.start
      ? ` · lines ${mention.start}${mention.end ? `–${mention.end}` : ""}`
      : ""
  }`
  const className =
    "inline-flex max-w-full items-center gap-1 rounded-xs border border-(--acc-chat)/45 bg-(--acc-chat)/10 px-1 align-baseline font-mono text-[0.92em] leading-snug text-foreground hover:bg-(--acc-chat)/20"
  const body = (
    <>
      <FileIcon className="size-3 shrink-0" />
      <span className="min-w-0 truncate">{name}</span>
    </>
  )
  if (open)
    return (
      <button
        type="button"
        title={`${title}\nOpens in the computer's Files`}
        className={className}
        onClick={() => open(mention.path)}
      >
        {body}
      </button>
    )
  if (sandbox)
    return (
      <Link
        to="/e/$env/sandboxes/$id"
        params={{ env: sandbox.env, id: sandbox.sandboxId }}
        search={{ tab: "changes", file: mention.path }}
        title={`${title}\nOpens in the sandbox's Files`}
        className={className}
      >
        {body}
      </Link>
    )
  return (
    <span title={title} className={className}>
      {body}
    </span>
  )
}
