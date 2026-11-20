import { useMemo } from "react"
import {
  ArrowsLeftRightIcon,
  BrainIcon,
  CaretRightIcon,
  FileTextIcon,
  GlobeIcon,
  MagnifyingGlassIcon,
  PencilSimpleIcon,
  SwapIcon,
  TerminalWindowIcon,
  TrashIcon,
  WrenchIcon,
  type Icon,
} from "@phosphor-icons/react"
import { Badge } from "@/components/ui/badge"
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Spinner } from "@/components/ui/spinner"
import { CodeBlock } from "@/components/chat/markdown"
import { Tokens, useDiffTokens } from "@/components/code-view"
import type { components } from "@/lib/api/schema.gen"
import type { ToolBody } from "@/lib/chats"
import { cn } from "@/lib/utils"

type ToolContent = components["schemas"]["ToolCallContent"]
type Location = components["schemas"]["ToolCallLocation"]

/** What each ACP tool kind does, in words and as an icon. */
export const toolKinds: Record<string, { icon: Icon; label: string }> = {
  read: { icon: FileTextIcon, label: "Read" },
  edit: { icon: PencilSimpleIcon, label: "Edit" },
  delete: { icon: TrashIcon, label: "Delete" },
  move: { icon: ArrowsLeftRightIcon, label: "Move" },
  search: { icon: MagnifyingGlassIcon, label: "Search" },
  execute: { icon: TerminalWindowIcon, label: "Run a command" },
  think: { icon: BrainIcon, label: "Think" },
  fetch: { icon: GlobeIcon, label: "Fetch" },
  switch_mode: { icon: SwapIcon, label: "Switch mode" },
  other: { icon: WrenchIcon, label: "Tool" },
}

export function toolKind(kind?: string) {
  return toolKinds[kind ?? ""] ?? toolKinds.other
}

const statuses: Record<
  NonNullable<ToolBody["status"]>,
  {
    label: string
    variant: "secondary" | "outline" | "destructive"
  }
> = {
  pending: { label: "Waiting", variant: "outline" },
  in_progress: { label: "Working", variant: "secondary" },
  completed: { label: "Done", variant: "outline" },
  failed: { label: "Failed", variant: "destructive" },
  stopped: { label: "Stopped", variant: "outline" },
}

/**
 * A tool call: kind icon, title and status, with the details (changes, output, files)
 * folded away. A running call's status keeps updating in place. `line` draws it as one quiet
 * line, as inside a fold of steps, where a finished call needs no badge: a failed one says so
 * in red, and an edit says how many lines it added and removed.
 */
export function ToolCallCard({
  tool,
  line = false,
  live = false,
  defaultOpen = false,
}: {
  tool: ToolBody
  line?: boolean
  /** The step a fold is on now: its title shimmers while the call runs. */
  live?: boolean
  defaultOpen?: boolean
}) {
  const kind = toolKind(tool.kind)
  const status = statuses[tool.status ?? "pending"]
  const content = tool.content ?? []
  const locations = tool.locations ?? []
  const hasBody = content.length > 0 || locations.length > 0 || tool.truncated
  const counts = useMemo(
    () => (line ? diffCounts(content) : undefined),
    [line, content]
  )
  return (
    <Collapsible
      defaultOpen={defaultOpen && !!hasBody}
      className={cn(
        "group/tool min-w-0 text-xs",
        line ? "grid gap-1" : "rounded-lg border bg-card"
      )}
    >
      <CollapsibleTrigger
        disabled={!hasBody}
        className={cn(
          "flex w-full min-w-0 items-center gap-2 text-left outline-none focus-visible:ring-2 focus-visible:ring-ring/30 disabled:cursor-default",
          line
            ? "rounded-sm py-0.5 text-muted-foreground enabled:hover:text-foreground"
            : "px-2.5 py-1.5"
        )}
      >
        <kind.icon
          className="size-3.5 shrink-0 text-muted-foreground"
          aria-label={kind.label}
        />
        <span
          className={cn(
            "min-w-0 flex-1 truncate",
            live &&
              (tool.status === "pending" || tool.status === "in_progress") &&
              "shimmer"
          )}
          title={tool.title}
        >
          {tool.title || kind.label}
        </span>
        {counts && (counts.added > 0 || counts.removed > 0) && (
          <span className="flex shrink-0 gap-1 text-caption">
            <LineCounts added={counts.added} removed={counts.removed} />
          </span>
        )}
        {line ? (
          tool.status === "in_progress" ? (
            <Spinner className="size-3 shrink-0" aria-label="Working" />
          ) : tool.status !== "completed" ? (
            <span
              className={cn(
                "shrink-0",
                tool.status === "failed" && "font-medium text-destructive"
              )}
            >
              {status.label}
            </span>
          ) : null
        ) : (
          <Badge variant={status.variant} className="shrink-0">
            {tool.status === "in_progress" && <Spinner className="size-2.5" />}
            {status.label}
          </Badge>
        )}
        <CaretRightIcon
          className={cn(
            "size-3 shrink-0 text-muted-foreground transition-transform group-data-[open]/tool:rotate-90",
            !hasBody && "invisible"
          )}
        />
      </CollapsibleTrigger>
      <CollapsibleContent
        className={line ? "rounded-lg border bg-card" : "border-t"}
      >
        <ScrollArea className="min-h-0" viewportClassName="max-h-80">
          <div className="grid min-w-0 gap-2 p-2.5">
            {locations.length > 0 && <Locations list={locations} />}
            {content.map((c, i) => (
              <ToolContentView key={i} content={c} />
            ))}
            {tool.truncated && (
              <p className="text-muted-foreground">
                The rest was too long to keep.
              </p>
            )}
          </div>
        </ScrollArea>
      </CollapsibleContent>
    </Collapsible>
  )
}

/** Lines added and removed, as "+3 −2", leaving out a side with none. */
export function LineCounts({
  added,
  removed,
}: {
  added: number
  removed: number
}) {
  return (
    <>
      {added > 0 && (
        <span className="text-emerald-600 dark:text-emerald-400">+{added}</span>
      )}
      {removed > 0 && <span className="text-destructive">−{removed}</span>}
    </>
  )
}

/** The lines a call's changes add and remove, over all its files. */
function diffCounts(content: ToolContent[]) {
  let added = 0
  let removed = 0
  for (const c of content) {
    if (c.type !== "diff") continue
    for (const r of diffLines(c.oldText ?? "", c.newText ?? "")) {
      if (r.op === "+") added++
      else if (r.op === "-") removed++
    }
  }
  return { added, removed }
}

export function Locations({ list }: { list: Location[] }) {
  return (
    <ul className="grid gap-0.5 font-mono text-caption text-muted-foreground">
      {list.map((l, i) => (
        <li key={i} className="truncate" title={l.path}>
          {l.path}
          {l.line ? `:${l.line}` : ""}
        </li>
      ))}
    </ul>
  )
}

function ToolContentView({ content: c }: { content: ToolContent }) {
  if (c.type === "diff") {
    return <Diff path={c.path} oldText={c.oldText} newText={c.newText ?? ""} />
  }
  if (c.type === "terminal") {
    return (
      <p className="text-muted-foreground">Ran in the agent's own terminal.</p>
    )
  }
  const block = c.content
  if (!block) return null
  if (block.type === "text" && block.text !== undefined) {
    return <CodeBlock text={block.text} className="whitespace-pre-wrap" />
  }
  if (block.type === "image" && block.data && block.mimeType) {
    return (
      <img
        alt=""
        className="max-h-60 max-w-full rounded-md border object-contain"
        src={`data:${block.mimeType};base64,${block.data}`}
      />
    )
  }
  if (block.type === "resource_link" && block.uri) {
    return (
      <p className="truncate font-mono text-muted-foreground" title={block.uri}>
        {block.name || block.uri}
      </p>
    )
  }
  return null
}

/** A file change as removed and added lines, highlighted by the file's language. */
export function Diff({
  path,
  oldText,
  newText,
}: {
  path?: string
  oldText?: string
  newText: string
}) {
  // A card shows the start of a long change; the counts are the whole change's.
  const { rows, hidden, added, removed } = useMemo(() => {
    const all = diffLines(oldText ?? "", newText)
    const changed = (r: Row) => r.op === "+" || r.op === "-"
    return {
      rows: all.slice(0, maxRows),
      hidden: all.slice(maxRows).filter(changed).length,
      added: all.filter((r) => r.op === "+").length,
      removed: all.filter((r) => r.op === "-").length,
    }
  }, [oldText, newText])
  const tokens = useDiffTokens(
    useMemo(
      () =>
        rows.map((r) =>
          path && r.op !== "…"
            ? { text: r.op + r.text, file: path }
            : { text: "" }
        ),
      [rows, path]
    )
  )
  return (
    <div className="min-w-0 overflow-hidden rounded-md border">
      <div className="flex items-center gap-2 border-b bg-muted/50 px-2 py-1 font-mono text-caption">
        <span className="min-w-0 flex-1 truncate" title={path}>
          {path ?? "file"}
        </span>
        {oldText === undefined ? (
          <span className="text-emerald-700 dark:text-emerald-400">new</span>
        ) : null}
        <span className="text-emerald-700 dark:text-emerald-400">+{added}</span>
        <span className="text-destructive">−{removed}</span>
      </div>
      <pre className="overflow-x-auto py-1 font-mono text-caption leading-relaxed">
        {rows.map((r, i) =>
          r.op === "…" ? (
            <div key={i} className="px-2 text-muted-foreground">
              ⋯
            </div>
          ) : (
            <div
              key={i}
              className={cn(
                "px-2",
                r.op === "+" && "bg-emerald-500/10",
                r.op === "-" && "bg-destructive/10",
                // A highlighted line keeps its syntax's colors.
                !tokens.get(i) &&
                  r.op === "+" &&
                  "text-emerald-800 dark:text-emerald-300",
                !tokens.get(i) && r.op === "-" && "text-destructive"
              )}
            >
              <span
                className={cn(
                  "select-none",
                  r.op === "+" && "text-emerald-800 dark:text-emerald-300",
                  r.op === "-" && "text-destructive"
                )}
              >
                {r.op}{" "}
              </span>
              {tokens.get(i) ? <Tokens tokens={tokens.get(i)!} /> : r.text}
            </div>
          )
        )}
        {hidden > 0 && (
          <div className="px-2 pt-1 font-sans text-muted-foreground">
            {hidden === 1
              ? "1 more changed line"
              : `${hidden} more changed lines`}{" "}
            not shown
          </div>
        )}
      </pre>
    </div>
  )
}

type Row = { op: "+" | "-" | " " | "…"; text: string }

/** Lines of context kept around each change. */
const context = 2

/** The most rows a change's card shows. */
const maxRows = 2000

/** The most changed lines a diff looks for; a larger change shows as removed and added. */
const maxEdits = 1000

/**
 * A line diff, with unchanged runs cut to a little context. The lines both texts start and end
 * with stay out of the comparison; between them Myers' diff finds the fewest lines removed and
 * added, removals first, so its cost grows with the size of the change rather than the file's.
 * A change of more than maxEdits lines shows its old lines removed and its new ones added.
 */
export function diffLines(a: string, b: string): Row[] {
  const all = (t: string) => (t === "" ? [] : t.replace(/\n$/, "").split("\n"))
  const before = all(a)
  const after = all(b)
  let head = 0
  while (
    head < before.length &&
    head < after.length &&
    before[head] === after[head]
  )
    head++
  let tail = 0
  while (
    tail < before.length - head &&
    tail < after.length - head &&
    before[before.length - 1 - tail] === after[after.length - 1 - tail]
  )
    tail++
  const x = before.slice(head, before.length - tail)
  const y = after.slice(head, after.length - tail)
  const same = (text: string): Row => ({ op: " ", text })
  let rows = myers(x, y, maxEdits) ?? [
    ...x.map((text) => ({ op: "-" as const, text })),
    ...y.map((text) => ({ op: "+" as const, text })),
  ]
  rows = [
    ...before.slice(0, head).map(same),
    ...rows,
    ...before.slice(before.length - tail).map(same),
  ]
  const keep = rows.map((_, i) =>
    rows
      .slice(Math.max(0, i - context), i + context + 1)
      .some((n) => n.op !== " ")
  )
  const out: Row[] = []
  rows.forEach((r, i) => {
    if (keep[i]) out.push(r)
    else if (out.at(-1)?.op !== "…") out.push({ op: "…", text: "" })
  })
  return out
}

/**
 * Whether round d reaches diagonal k down from k+1, a line added, rather than right from k−1, a
 * line removed; at is how far the previous round reached on a diagonal. On a tie it goes right,
 * so removals come before what replaces them.
 */
function down(at: (k: number) => number, k: number, d: number) {
  return k === -d || (k !== d && at(k - 1) < at(k + 1))
}

/**
 * Myers' O(ND) diff of x and y, or undefined if it takes more than limit lines removed and
 * added. v[k] is how far along x the furthest path on diagonal k (x index − y index) reaches;
 * each round d keeps a copy of its v, from −d to d, to walk the path back.
 */
function myers(x: string[], y: string[], limit: number): Row[] | undefined {
  const n = x.length
  const m = y.length
  const off = n + m + 1
  const v = new Int32Array(2 * off + 1)
  const trace: Int32Array[] = []
  for (let d = 0; d <= Math.min(n + m, limit); d++) {
    for (let k = -d; k <= d; k += 2) {
      let i = down((j) => v[off + j], k, d)
        ? v[off + k + 1]
        : v[off + k - 1] + 1
      let j = i - k
      while (i < n && j < m && x[i] === y[j]) {
        i++
        j++
      }
      v[off + k] = i
      if (i >= n && j >= m) return walkBack(x, y, trace, d)
    }
    trace.push(v.slice(off - d, off + d + 1))
  }
  return undefined
}

/** The rows of the path myers found in round last, walked back through its rounds' copies. */
function walkBack(
  x: string[],
  y: string[],
  trace: Int32Array[],
  last: number
): Row[] {
  const rows: Row[] = []
  let i = x.length
  let j = y.length
  for (let d = last; d > 0; d--) {
    const prev = trace[d - 1]
    const at = (n: number) => prev[n + d - 1]
    const k = i - j
    const added = down(at, k, d)
    const fromK = added ? k + 1 : k - 1
    const fromI = at(fromK)
    const fromJ = fromI - fromK
    // The unchanged run after this round's one change, then the change.
    while (i > (added ? fromI : fromI + 1)) rows.push({ op: " ", text: x[--i] })
    rows.push(added ? { op: "+", text: y[fromJ] } : { op: "-", text: x[fromI] })
    i = fromI
    j = fromJ
  }
  while (i > 0) rows.push({ op: " ", text: x[--i] })
  return rows.reverse()
}
