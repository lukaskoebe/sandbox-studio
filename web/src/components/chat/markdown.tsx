import type { ReactNode } from "react"
import { cn } from "@/lib/utils"

/**
 * A small markdown renderer for agent messages: headings, paragraphs, lists, quotes, code
 * blocks, rules, and inline code, bold, italics and links. It builds React elements and
 * never HTML, so agent text can't inject markup. Anything it doesn't know stays plain text.
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
  | { kind: "para"; text: string }

/** A match's groups; a group that did not take part is undefined. */
type Groups = (string | undefined)[] & { index: number }

const listItem = /^\s{0,3}(?:([-*+])|(\d{1,9})[.)])\s+(.*)$/

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
      !listItem.test(lines[i])
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
      case "para":
        return (
          <p key={i} className="whitespace-pre-wrap">
            {inline(b.text)}
          </p>
        )
    }
  })
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
        "max-w-full overflow-x-auto rounded-md bg-muted px-2.5 py-2 font-mono text-[0.6875rem] leading-relaxed",
        className
      )}
    >
      <code>{text}</code>
    </pre>
  )
}

// Inline code first, so markup inside backticks stays literal.
const inlinePattern =
  /(`+)([\s\S]*?[^`])\1(?!`)|\*\*([\s\S]+?)\*\*|__([\s\S]+?)__|(?<![\w*])\*(?!\s)([\s\S]+?)(?<!\s)\*(?!\w)|(?<!\w)_(?!\s)([\s\S]+?)(?<!\s)_(?!\w)|\[([^\]]+)\]\(([^)\s]+)\)|(https?:\/\/[^\s<>()]+[^\s<>().,;:!?'"])/

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
      out.push(
        <code
          key={k}
          className="rounded bg-muted px-1 py-px font-mono text-[0.6875rem]"
        >
          {m[2].trim() || m[2]}
        </code>
      )
    } else if (m[3] !== undefined || m[4] !== undefined) {
      out.push(<strong key={k}>{inline(m[3] ?? m[4] ?? "")}</strong>)
    } else if (m[5] !== undefined || m[6] !== undefined) {
      out.push(<em key={k}>{inline(m[5] ?? m[6] ?? "")}</em>)
    } else if (m[7] !== undefined) {
      out.push(
        <SafeLink key={k} href={m[8] ?? ""}>
          {inline(m[7])}
        </SafeLink>
      )
    } else {
      out.push(
        <SafeLink key={k} href={m[9] ?? ""}>
          {m[9]}
        </SafeLink>
      )
    }
    rest = rest.slice(m.index + (m[0] ?? "").length)
  }
  return out
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
