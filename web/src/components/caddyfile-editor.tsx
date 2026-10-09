import { useLayoutEffect, useRef } from "react"
import {
  autocompletion,
  closeCompletion,
  completionKeymap,
  type Completion,
  type CompletionContext,
  type CompletionResult,
} from "@codemirror/autocomplete"
import {
  defaultKeymap,
  history,
  historyKeymap,
  indentWithTab,
} from "@codemirror/commands"
import {
  HighlightStyle,
  StreamLanguage,
  bracketMatching,
  indentOnInput,
  indentUnit,
  syntaxHighlighting,
} from "@codemirror/language"
import {
  linter,
  lintGutter,
  setDiagnostics,
  setDiagnosticsEffect,
  type Diagnostic,
} from "@codemirror/lint"
import { Annotation, EditorState, Prec, Transaction } from "@codemirror/state"
import {
  EditorView,
  highlightActiveLine,
  highlightActiveLineGutter,
  keymap,
  lineNumbers,
} from "@codemirror/view"
import { tags } from "@lezer/highlight"
import { KeyIcon } from "@phosphor-icons/react"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"

type SecretName = { name: string }
type CaddyfileState = { depth: number; quote: string | null }

const directives = new Set([
  "abort",
  "basic_auth",
  "copy_response",
  "copy_response_headers",
  "error",
  "expression",
  "file_server",
  "handle",
  "handle_errors",
  "handle_path",
  "header",
  "header_down",
  "header_regexp",
  "header_up",
  "host",
  "log",
  "map",
  "method",
  "not",
  "path",
  "path_regexp",
  "protocol",
  "query",
  "redir",
  "request_body",
  "request_header",
  "respond",
  "reverse_proxy",
  "rewrite",
  "route",
  "root",
  "static_response",
  "try_files",
  "uri",
  "vars",
])

const caddyfileLanguage = StreamLanguage.define<CaddyfileState>({
  name: "Caddyfile",
  startState: () => ({ depth: 0, quote: null }),
  token(stream, state) {
    if (!state.quote && stream.eatSpace()) return null

    if (state.quote) {
      if (stream.peek() === state.quote) {
        stream.next()
        state.quote = null
        return "string"
      }
      if (stream.peek() === "{") {
        if (stream.match(/\{[^{}\s]+\}/)) return "variableName.special"
        stream.next()
        return "string"
      }

      while (
        !stream.eol() &&
        stream.peek() !== state.quote &&
        stream.peek() !== "{"
      ) {
        const char = stream.next()
        if (char === "\\" && state.quote !== "`" && !stream.eol()) stream.next()
      }
      return "string"
    }

    const next = stream.peek()
    if (next === "#") {
      stream.skipToEnd()
      return "comment"
    }
    if (next === '"' || next === "'" || next === "`") {
      state.quote = stream.next() ?? null
      return "string"
    }
    if (stream.match(/\{[^{}\s]+\}/)) return "variableName.special"
    if (next === "{") {
      stream.next()
      state.depth += 1
      return "punctuation"
    }
    if (next === "}") {
      stream.next()
      state.depth = Math.max(0, state.depth - 1)
      return "punctuation"
    }
    if (next === "@") {
      stream.next()
      stream.eatWhile(/[\w.-]/)
      return "meta"
    }
    if (stream.eatWhile(/[\w.-]/)) {
      const word = stream.current()
      return directives.has(word) ? "keyword" : "atom"
    }
    if (/[=<>|&!]/.test(next ?? "")) {
      stream.next()
      stream.eatWhile(/[=<>|&!]/)
      return "operator"
    }

    stream.next()
    return "atom"
  },
  indent(state, textAfter, { unit }) {
    const dedent = textAfter.trimStart().startsWith("}") ? 1 : 0
    return Math.max(0, state.depth - dedent) * unit
  },
  languageData: { commentTokens: { line: "#" } },
})

const caddyfileHighlight = HighlightStyle.define([
  { tag: tags.comment, color: "var(--muted-foreground)" },
  { tag: tags.keyword, color: "var(--primary)", fontWeight: "600" },
  { tag: tags.meta, color: "var(--chart-3)", fontWeight: "600" },
  { tag: tags.string, color: "var(--chart-4)" },
  {
    tag: tags.special(tags.variableName),
    color: "var(--chart-2)",
    fontWeight: "500",
  },
  { tag: tags.operator, color: "var(--chart-5)" },
  { tag: tags.punctuation, color: "var(--muted-foreground)" },
])

const editorTheme = EditorView.theme({
  "&": {
    backgroundColor: "var(--background)",
    color: "var(--foreground)",
    fontSize: "0.75rem",
  },
  "&.cm-focused": {
    outline: "2px solid color-mix(in oklch, var(--ring) 35%, transparent)",
    outlineOffset: "-2px",
  },
  ".cm-scroller": {
    maxHeight: "24rem",
    overflow: "auto",
    fontFamily:
      "ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace",
    lineHeight: "1.55",
  },
  ".cm-content": { minHeight: "9rem", padding: "0.65rem 0.75rem" },
  ".cm-gutters": {
    backgroundColor: "var(--background)",
    color: "var(--muted-foreground)",
    borderRight: "1px solid var(--border)",
  },
  ".cm-activeLine": {
    backgroundColor: "color-mix(in oklch, var(--accent) 65%, transparent)",
  },
  ".cm-activeLineGutter": {
    color: "var(--foreground)",
    backgroundColor: "color-mix(in oklch, var(--accent) 65%, transparent)",
  },
  ".cm-lintRange-error": {
    backgroundImage:
      "linear-gradient(to bottom, transparent calc(100% - 1px), var(--destructive) 0)",
  },
  ".cm-gutterElement.cm-lintPoint-error": {
    borderLeft: "2px solid var(--destructive)",
  },
  ".cm-tooltip": {
    backgroundColor: "var(--popover)",
    color: "var(--popover-foreground)",
    border: "1px solid var(--border)",
    borderRadius: "var(--radius-md)",
  },
  ".cm-tooltip-autocomplete ul": {
    fontFamily:
      "ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace",
  },
  ".cm-tooltip-autocomplete ul li[aria-selected]": {
    backgroundColor: "var(--accent)",
    color: "var(--accent-foreground)",
  },
})

const externalValueSync = Annotation.define<boolean>()

function secretCompletion(secretsRef: {
  current: readonly SecretName[]
}): (context: CompletionContext) => CompletionResult | null {
  return (context) => {
    const match = context.matchBefore(/\{secret\.[A-Za-z0-9_]*$/)
    if (!match || secretsRef.current.length === 0) return null

    return {
      from: match.from,
      to: context.pos,
      validFor: /^\{secret\.[A-Za-z0-9_]*$/,
      options: secretsRef.current.map(({ name }) => {
        const placeholder = `{secret.${name}}`
        return {
          label: placeholder,
          type: "variable",
          apply(
            view: EditorView,
            _completion: Completion,
            from: number,
            to: number
          ) {
            const hasClosingBrace =
              view.state.doc.sliceString(to, to + 1) === "}"
            const end = hasClosingBrace ? to + 1 : to
            view.dispatch({
              changes: { from, to: end, insert: placeholder },
              selection: { anchor: from + placeholder.length },
              scrollIntoView: true,
              annotations: Transaction.userEvent.of("input.complete"),
            })
          },
        }
      }),
    }
  }
}

const keyBindings = [
  ...completionKeymap.map((binding) =>
    binding.key === "Escape"
      ? {
          ...binding,
          run: closeCompletion,
          stopPropagation: true,
        }
      : binding
  ),
  ...historyKeymap,
  indentWithTab,
  ...defaultKeymap,
]

function diagnosticsFor(
  error: string | undefined,
  state: EditorState
): Diagnostic[] {
  const match = error?.match(/\bline\s+(\d+)\b/i)
  if (!error || !match) return []

  const lineNumber = Number(match[1])
  if (
    !Number.isSafeInteger(lineNumber) ||
    lineNumber < 1 ||
    lineNumber > state.doc.lines
  )
    return []

  const line = state.doc.line(lineNumber)
  return [
    {
      from: line.from,
      to: line.to,
      severity: "error",
      source: "Caddyfile",
      message: error,
    },
  ]
}

export function CaddyfileEditor({
  value,
  onChange,
  secrets,
  error,
}: {
  value: string
  onChange: (value: string) => void
  secrets: readonly SecretName[]
  error?: string
}) {
  const hostRef = useRef<HTMLDivElement>(null)
  const editorRef = useRef<EditorView | null>(null)
  const initialValueRef = useRef(value)
  const onChangeRef = useRef(onChange)
  const secretsRef = useRef(secrets)

  onChangeRef.current = onChange
  secretsRef.current = secrets

  useLayoutEffect(() => {
    const host = hostRef.current
    if (!host) return

    const view = new EditorView({
      state: EditorState.create({
        doc: initialValueRef.current,
        extensions: [
          caddyfileLanguage,
          syntaxHighlighting(caddyfileHighlight),
          editorTheme,
          lineNumbers(),
          EditorView.lineWrapping,
          highlightActiveLineGutter(),
          highlightActiveLine(),
          history(),
          bracketMatching(),
          indentUnit.of("  "),
          indentOnInput(),
          autocompletion({
            defaultKeymap: false,
            override: [secretCompletion(secretsRef)],
          }),
          linter(null),
          lintGutter(),
          EditorState.transactionExtender.of((transaction) =>
            transaction.docChanged
              ? { effects: setDiagnosticsEffect.of([]) }
              : null
          ),
          EditorView.contentAttributes.of({
            "aria-label": "Caddyfile",
            spellcheck: "false",
            tabindex: "0",
          }),
          EditorView.updateListener.of((update) => {
            if (!update.docChanged) return

            const isExternalSync = update.transactions.some((transaction) =>
              transaction.annotation(externalValueSync)
            )
            if (!isExternalSync)
              onChangeRef.current(update.state.doc.toString())
          }),
          Prec.highest(keymap.of(keyBindings)),
        ],
      }),
      parent: host,
    })

    editorRef.current = view
    return () => {
      view.destroy()
      if (editorRef.current === view) editorRef.current = null
    }
  }, [])

  useLayoutEffect(() => {
    const view = editorRef.current
    if (!view || view.state.doc.toString() === value) return

    const previous = view.state.doc.toString()
    let from = 0
    while (
      from < previous.length &&
      from < value.length &&
      previous.charCodeAt(from) === value.charCodeAt(from)
    ) {
      from += 1
    }

    let previousEnd = previous.length
    let valueEnd = value.length
    while (
      previousEnd > from &&
      valueEnd > from &&
      previous.charCodeAt(previousEnd - 1) === value.charCodeAt(valueEnd - 1)
    ) {
      previousEnd -= 1
      valueEnd -= 1
    }

    view.dispatch({
      changes: {
        from,
        to: previousEnd,
        insert: value.slice(from, valueEnd),
      },
      annotations: [
        externalValueSync.of(true),
        Transaction.addToHistory.of(false),
      ],
    })
  }, [value])

  useLayoutEffect(() => {
    const view = editorRef.current
    if (view) {
      view.dispatch(
        setDiagnostics(view.state, diagnosticsFor(error, view.state))
      )
    }
  }, [error])

  const insertSecret = (name: string) => {
    const view = editorRef.current
    if (!view) return

    const { from, to } = view.state.selection.main
    const placeholder = `{secret.${name}}`
    view.dispatch({
      changes: { from, to, insert: placeholder },
      selection: { anchor: from + placeholder.length },
      scrollIntoView: true,
      annotations: Transaction.userEvent.of("input"),
    })
    view.focus()
  }

  return (
    <div className="min-w-0 space-y-1.5">
      {secrets.length > 0 && (
        <div className="flex justify-end">
          <DropdownMenu>
            <DropdownMenuTrigger
              render={
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  aria-label="Insert secret"
                />
              }
            >
              <KeyIcon />
              Insert secret
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="min-w-40">
              <DropdownMenuGroup>
                <DropdownMenuLabel>Insert a secret</DropdownMenuLabel>
                {secrets.map(({ name }) => (
                  <DropdownMenuItem
                    key={name}
                    className="font-mono"
                    onClick={() => insertSecret(name)}
                  >
                    {name}
                  </DropdownMenuItem>
                ))}
              </DropdownMenuGroup>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      )}
      <div
        ref={hostRef}
        className="w-full min-w-0 overflow-hidden rounded-md border border-input bg-background"
      />
    </div>
  )
}
