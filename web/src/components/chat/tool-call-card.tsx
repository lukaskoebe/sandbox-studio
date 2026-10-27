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
 * folded away. A running call's status keeps updating in place.
 */
export function ToolCallCard({ tool }: { tool: ToolBody }) {
  const kind = toolKind(tool.kind)
  const status = statuses[tool.status ?? "pending"]
  const content = tool.content ?? []
  const locations = tool.locations ?? []
  const hasBody = content.length > 0 || locations.length > 0 || tool.truncated
  return (
    <Collapsible className="group/tool min-w-0 rounded-lg border bg-card text-xs">
      <CollapsibleTrigger
        disabled={!hasBody}
        className="flex w-full min-w-0 items-center gap-2 px-2.5 py-1.5 text-left outline-none focus-visible:ring-2 focus-visible:ring-ring/30 disabled:cursor-default"
      >
        <kind.icon
          className="size-3.5 shrink-0 text-muted-foreground"
          aria-label={kind.label}
        />
        <span className="min-w-0 flex-1 truncate" title={tool.title}>
          {tool.title || kind.label}
        </span>
        <Badge variant={status.variant} className="shrink-0">
          {tool.status === "in_progress" && <Spinner className="size-2.5" />}
          {status.label}
        </Badge>
        <CaretRightIcon
          className={cn(
            "size-3 shrink-0 text-muted-foreground transition-transform group-data-[open]/tool:rotate-90",
            !hasBody && "invisible"
          )}
        />
      </CollapsibleTrigger>
      <CollapsibleContent className="border-t">
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
  const rows = useMemo(
    () => diffLines(oldText ?? "", newText),
    [oldText, newText]
  )
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
  const added = rows.filter((r) => r.op === "+").length
  const removed = rows.filter((r) => r.op === "-").length
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
                !tokens.has(i) &&
                  r.op === "+" &&
                  "text-emerald-800 dark:text-emerald-300",
                !tokens.has(i) && r.op === "-" && "text-destructive"
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
              {tokens.has(i) ? <Tokens tokens={tokens.get(i)!} /> : r.text}
            </div>
          )
        )}
      </pre>
    </div>
  )
}

type Row = { op: "+" | "-" | " " | "…"; text: string }

/** Lines of context kept around each change. */
const context = 2

/**
 * A line diff by longest common subsequence, with unchanged runs cut to a little context.
 * Large files fall back to the whole old text removed and the new one added.
 */
export function diffLines(a: string, b: string): Row[] {
  const x = a === "" ? [] : a.replace(/\n$/, "").split("\n")
  const y = b === "" ? [] : b.replace(/\n$/, "").split("\n")
  let rows: Row[]
  if (x.length * y.length > 250_000) {
    rows = [
      ...x.map((text) => ({ op: "-" as const, text })),
      ...y.map((text) => ({ op: "+" as const, text })),
    ]
  } else {
    // lcs[i][j]: length of the common subsequence of x[i:] and y[j:].
    const lcs = Array.from({ length: x.length + 1 }, () =>
      new Array<number>(y.length + 1).fill(0)
    )
    for (let i = x.length - 1; i >= 0; i--)
      for (let j = y.length - 1; j >= 0; j--)
        lcs[i][j] =
          x[i] === y[j]
            ? lcs[i + 1][j + 1] + 1
            : Math.max(lcs[i + 1][j], lcs[i][j + 1])
    rows = []
    let i = 0
    let j = 0
    while (i < x.length || j < y.length) {
      if (i < x.length && j < y.length && x[i] === y[j]) {
        rows.push({ op: " ", text: x[i] })
        i++
        j++
      } else if (
        j < y.length &&
        (i >= x.length || lcs[i][j + 1] >= lcs[i + 1][j])
      ) {
        rows.push({ op: "+", text: y[j++] })
      } else {
        rows.push({ op: "-", text: x[i++] })
      }
    }
  }
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
