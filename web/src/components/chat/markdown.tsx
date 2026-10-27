import { createContext, useContext, type ReactNode } from "react"
import { BroadcastIcon } from "@phosphor-icons/react"
import { openPreviewTab, parseSandboxLocalLink } from "@/lib/previews"
import { cn } from "@/lib/utils"

/**
 * The sandbox whose servers a chat's links to localhost point at. Inside it they open as
 * Studio previews, or stay plain text while previews are off; elsewhere they stay as written.
 */
export const PreviewSandbox = createContext<{
  env: string
  sandboxId: string
  enabled: boolean
} | null>(null)

/**
 * Renders [[slug]] and [[slug|text]] links, as memory pages write them. Without it they stay
 * as written.
 */
export const WikiLinks = createContext<
  ((target: string, label: string) => ReactNode) | null
>(null)

/**
 * A small markdown renderer for agent messages: headings, paragraphs, lists, quotes, code
 * blocks, tables, rules, and inline code, bold, italics and links. It builds React elements and
 * never HTML, so agent text can't inject markup. Anything it doesn't know stays plain text.
 * Links to localhost:<port> open the sandbox's preview inside a PreviewSandbox.
 */
export function Markdown({
  text,
  className,
}: {
  text: string
  className?: string
}) {
  return (
    <div className={cn("grid min-w-0 gap-2 text-xs/relaxed", className)}>
      {blocks(text)}
    </div>
  )
}

type Block =
  | { kind: "code"; lang: string; text: string }
  | { kind: "heading"; level: number; text: string }
  | { kind: "list"; ordered: boolean; start: number; items: string[] }
  | { kind: "quote"; text: string }
  | { kind: "rule" }
  | { kind: "table"; align: Align[]; head: string[]; rows: string[][] }
  | { kind: "para"; text: string }

type Align = "left" | "center" | "right" | undefined

/** A match's groups; a group that did not take part is undefined. */
type Groups = (string | undefined)[] & { index: number }

const listItem = /^\s{0,3}(?:([-*+])|(\d{1,9})[.)])\s+(.*)$/

// A table's delimiter row, as in GitHub's markdown: |---|:---:|, with at least one pipe.
const delimiterRow = /^\s{0,3}\|?\s*:?-+:?\s*(\|\s*:?-+:?\s*)*\|?\s*$/

/** Whether lines[i] starts a table: a row with pipes over a delimiter row of as many cells. */
function tableAt(lines: string[], i: number) {
  if (i + 1 >= lines.length) return false
  const next = lines[i + 1]
  return (
    lines[i].includes("|") &&
    next.includes("|") &&
    delimiterRow.test(next) &&
    cells(lines[i]).length === cells(next).length
  )
}

/**
 * A table row's cells. \| is a pipe in a cell, and so is one inside a code span, which agents
 * write for shell pipes; the outer pipes are optional.
 */
function cells(line: string, codeSpans = true): string[] {
  let s = line.trim()
  if (s.startsWith("|")) s = s.slice(1)
  if (s.endsWith("|") && !s.endsWith("\\|")) s = s.slice(0, -1)
  const out: string[] = []
  let cell = ""
  let ticks = 0 // the length of the open code span's backtick run
  for (let j = 0; j < s.length; j++) {
    const c = s[j]
    if (c === "\\" && s[j + 1] === "|") {
      cell += "|"
      j++
    } else if (c === "`" && codeSpans) {
      let n = 1
      while (s[j + n] === "`") n++
      if (ticks === 0) ticks = n
      else if (ticks === n) ticks = 0
      cell += "`".repeat(n)
      j += n - 1
    } else if (c === "|" && ticks === 0) {
      out.push(cell.trim())
      cell = ""
    } else cell += c
  }
  out.push(cell.trim())
  // A backtick that never closes isn't a code span: split at every pipe instead.
  return ticks && codeSpans ? cells(line, false) : out
}

function alignOf(delimiter: string): Align {
  const left = delimiter.startsWith(":")
  const right = delimiter.endsWith(":")
  return left && right ? "center" : right ? "right" : left ? "left" : undefined
}

function parse(text: string): Block[] {
  const lines = text.replace(/\r\n?/g, "\n").split("\n")
  const out: Block[] = []
  let i = 0
  while (i < lines.length) {
    const line = lines[i]
    const fence = /^\s{0,3}(```+|~~~+)\s*([\w+-]*)/.exec(line)
    if (fence) {
      const body: string[] = []
      i++
      while (i < lines.length && !lines[i].trimStart().startsWith(fence[1]))
        body.push(lines[i++])
      i++
      out.push({ kind: "code", lang: fence[2], text: body.join("\n") })
      continue
    }
    if (!line.trim()) {
      i++
      continue
    }
    const heading = /^\s{0,3}(#{1,6})\s+(.*?)\s*#*\s*$/.exec(line)
    if (heading) {
      out.push({ kind: "heading", level: heading[1].length, text: heading[2] })
      i++
      continue
    }
    if (/^\s{0,3}([-*_])(\s*\1){2,}\s*$/.test(line)) {
      out.push({ kind: "rule" })
      i++
      continue
    }
    if (/^\s{0,3}>/.test(line)) {
      const body: string[] = []
      while (i < lines.length && /^\s{0,3}>/.test(lines[i]))
        body.push(lines[i++].replace(/^\s{0,3}>\s?/, ""))
      out.push({ kind: "quote", text: body.join("\n") })
      continue
    }
    if (tableAt(lines, i)) {
      const head = cells(line)
      const align = cells(lines[i + 1]).map(alignOf)
      const rows: string[][] = []
      i += 2
      while (i < lines.length && lines[i].trim() && lines[i].includes("|")) {
        const row = cells(lines[i++])
        rows.push(head.map((_, j) => row[j] ?? ""))
      }
      out.push({ kind: "table", align, head, rows })
      continue
    }
    const first: Groups | null = listItem.exec(line)
    if (first) {
      const ordered = first[2] !== undefined
      const items: string[] = []
      while (i < lines.length) {
        const m: Groups | null = listItem.exec(lines[i])
        if (m && (m[2] !== undefined) === ordered) {
          items.push(m[3] ?? "")
        } else if (/^\s{2,}\S/.test(lines[i]) && items.length) {
          // A continuation line belongs to the item above.
          items[items.length - 1] += "\n" + lines[i].trim()
        } else break
        i++
      }
      out.push({
        kind: "list",
        ordered,
        start: ordered ? Number(first[2]) : 1,
        items,
      })
      continue
    }
    const body: string[] = []
    while (
      i < lines.length &&
      lines[i].trim() &&
      !/^\s{0,3}(```|~~~|#{1,6}\s|>)/.test(lines[i]) &&
      !listItem.test(lines[i]) &&
      !tableAt(lines, i)
    )
      body.push(lines[i++])
    out.push({ kind: "para", text: body.join("\n") })
  }
  return out
}

function blocks(text: string): ReactNode[] {
  return parse(text).map((b, i) => {
    switch (b.kind) {
      case "code":
        return <CodeBlock key={i} text={b.text} />
      case "heading":
        return (
          <p
            key={i}
            className={cn(
              "font-semibold",
              b.level <= 2 ? "mt-1 text-sm" : "text-xs"
            )}
          >
            {inline(b.text)}
          </p>
        )
      case "list": {
        const items = b.items.map((it, j) => (
          <li key={j} className="pl-0.5">
            {inline(it)}
          </li>
        ))
        return b.ordered ? (
          <ol key={i} start={b.start} className="list-decimal space-y-1 pl-5">
            {items}
          </ol>
        ) : (
          <ul key={i} className="list-disc space-y-1 pl-5">
            {items}
          </ul>
        )
      }
      case "quote":
        return (
          <blockquote key={i} className="border-l-2 pl-3 text-muted-foreground">
            {blocks(b.text)}
          </blockquote>
        )
      case "rule":
        return <hr key={i} className="border-border" />
      case "table":
        return <Table key={i} {...b} />
      case "para":
        return (
          <p key={i} className="whitespace-pre-wrap">
            {inline(b.text)}
          </p>
        )
    }
  })
}

/** A table that scrolls sideways rather than widening the chat. */
function Table({
  align,
  head,
  rows,
}: {
  align: Align[]
  head: string[]
  rows: string[][]
}) {
  const cell = "max-w-80 px-2.5 py-1.5 align-top"
  return (
    <div className="max-w-full overflow-x-auto rounded-md border">
      <table className="min-w-full border-collapse">
        <thead className="bg-muted/60">
          <tr className="border-b">
            {head.map((h, j) => (
              <th
                key={j}
                className={cn(cell, "text-left font-medium")}
                style={{ textAlign: align[j] }}
              >
                {inline(h)}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((row, r) => (
            <tr key={r} className="border-b last:border-b-0">
              {row.map((c, j) => (
                <td key={j} className={cell} style={{ textAlign: align[j] }}>
                  {inline(c)}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

/** Preformatted text that scrolls sideways rather than widening the chat. */
export function CodeBlock({
  text,
  className,
}: {
  text: string
  className?: string
}) {
  return (
    <pre
      className={cn(
        "max-w-full overflow-x-auto rounded-md bg-muted px-2.5 py-2 font-mono text-caption leading-relaxed",
        className
      )}
    >
      <code>{text}</code>
    </pre>
  )
}

// Inline code first, so markup inside backticks stays literal. A link to localhost comes before
// other URLs, so http://localhost:5173 is one.
const inlinePattern =
  /(`+)([\s\S]*?[^`])\1(?!`)|\*\*([\s\S]+?)\*\*|__([\s\S]+?)__|(?<![\w*])\*(?!\s)([\s\S]+?)(?<!\s)\*(?!\w)|(?<!\w)_(?!\s)([\s\S]+?)(?<!\s)_(?!\w)|\[([^\]]+)\]\(([^)\s]+)\)|(?<![\w./@:-])((?:https?:\/\/)?(?:localhost|127\.0\.0\.1|0\.0\.0\.0|\[::1\]):\d{1,5}(?:\/(?:[^\s<>()]*[^\s<>().,;:!?'"])?)?)(?![\w:])|(https?:\/\/[^\s<>()]+[^\s<>().,;:!?'"])|\[\[([^[\]|\n]{1,128})(?:\|([^[\]\n]{1,200}))?\]\]/

function inline(text: string): ReactNode[] {
  const out: ReactNode[] = []
  let rest = text
  let key = 0
  while (rest) {
    const m: Groups | null = inlinePattern.exec(rest)
    if (!m) {
      out.push(rest)
      break
    }
    if (m.index > 0) out.push(rest.slice(0, m.index))
    const k = key++
    if (m[2] !== undefined) {
      const code = (
        <code
          key={k}
          className="rounded bg-muted px-1 py-px font-mono text-caption"
        >
          {m[2].trim() || m[2]}
        </code>
      )
      const local = parseSandboxLocalLink(m[2])
      out.push(
        local ? (
          <PreviewLink key={k} {...local}>
            {code}
          </PreviewLink>
        ) : (
          code
        )
      )
    } else if (m[3] !== undefined || m[4] !== undefined) {
      out.push(<strong key={k}>{inline(m[3] ?? m[4] ?? "")}</strong>)
    } else if (m[5] !== undefined || m[6] !== undefined) {
      out.push(<em key={k}>{inline(m[5] ?? m[6] ?? "")}</em>)
    } else if (m[7] !== undefined) {
      const local = parseSandboxLocalLink(m[8] ?? "")
      out.push(
        local ? (
          <PreviewLink key={k} {...local} href={m[8]}>
            {inline(m[7])}
          </PreviewLink>
        ) : (
          <SafeLink key={k} href={m[8] ?? ""}>
            {inline(m[7])}
          </SafeLink>
        )
      )
    } else if (m[9] !== undefined) {
      const local = parseSandboxLocalLink(m[9])
      out.push(
        local ? (
          <PreviewLink key={k} {...local} href={m[9]}>
            {m[9]}
          </PreviewLink>
        ) : (
          m[9]
        )
      )
    } else if (m[10] !== undefined) {
      out.push(
        <SafeLink key={k} href={m[10]}>
          {m[10]}
        </SafeLink>
      )
    } else {
      out.push(
        <WikiLink
          key={k}
          target={(m[11] ?? "").trim()}
          label={(m[12] ?? m[11] ?? "").trim()}
          raw={m[0] ?? ""}
        />
      )
    }
    rest = rest.slice(m.index + (m[0] ?? "").length)
  }
  return out
}

function WikiLink({
  target,
  label,
  raw,
}: {
  target: string
  label: string
  raw: string
}) {
  const render = useContext(WikiLinks)
  return <>{render ? render(target.toLowerCase(), label) : raw}</>
}

/** Links open in a new tab; anything but http(s) and mailto stays text. */
function SafeLink({ href, children }: { href: string; children: ReactNode }) {
  if (!/^(https?:|mailto:)/i.test(href)) return <>{children}</>
  return (
    <a
      href={href}
      target="_blank"
      rel="noreferrer noopener"
      className="underline underline-offset-3 hover:text-foreground"
    >
      {children}
    </a>
  )
}

/**
 * A link to a server in the sandbox, such as localhost:5173/login. The user's own localhost
 * is not the sandbox, so inside a chat it opens the port's preview, on that page, while
 * previews are on; elsewhere it stays as written, a link if it was one.
 */
function PreviewLink({
  port,
  path,
  href,
  children,
}: {
  port: number
  path?: string
  href?: string
  children: ReactNode
}) {
  const sandbox = useContext(PreviewSandbox)
  if (!sandbox)
    return href ? <SafeLink href={href}>{children}</SafeLink> : <>{children}</>
  // The user's own localhost is not the sandbox, so with previews off the link goes nowhere.
  if (!sandbox.enabled) return <>{children}</>
  return (
    <button
      type="button"
      title={`Open port ${port} of the sandbox in Studio's preview`}
      onClick={() =>
        void openPreviewTab(sandbox.env, sandbox.sandboxId, port, false, path)
      }
      className="inline-flex max-w-full items-center gap-1 rounded-md border bg-muted/40 px-1 align-baseline underline-offset-3 hover:bg-muted hover:underline [&_code]:bg-transparent [&_code]:p-0"
    >
      <BroadcastIcon className="size-3 shrink-0 text-muted-foreground" />
      <span className="min-w-0 truncate">{children}</span>
    </button>
  )
}
