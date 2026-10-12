import { useLayoutEffect, useRef } from "react"
import {
  autocompletion,
  type Completion,
  type CompletionContext,
  type CompletionResult,
} from "@codemirror/autocomplete"
import { history } from "@codemirror/commands"
import {
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
import { EditorState, Prec, Transaction } from "@codemirror/state"
import {
  EditorView,
  highlightActiveLine,
  highlightActiveLineGutter,
  keymap,
  lineNumbers,
} from "@codemirror/view"
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
import {
  codeEditorKeyBindings,
  codeEditorTheme,
  codeHighlightStyle,
  editorChangeListener,
  syncEditorValue,
} from "@/lib/code-editor"

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
      return directives.has(stream.current()) ? "keyword" : null
    }
    if (/[=<>|&!]/.test(next ?? "")) {
      stream.next()
      stream.eatWhile(/[=<>|&!]/)
      return "operator"
    }

    stream.next()
    return null
  },
  indent(state, textAfter, { unit }) {
    const dedent = textAfter.trimStart().startsWith("}") ? 1 : 0
    return Math.max(0, state.depth - dedent) * unit
  },
  languageData: { commentTokens: { line: "#" } },
})

function secretCompletion(secretsRef: {
  current: readonly SecretName[]
}): (context: CompletionContext) => CompletionResult | null {
  return (context) => {
    // Offer secrets from the first letter of {secret., not only once it's typed out.
    const match = context.matchBefore(/\{[A-Za-z0-9_.]*$/)
    if (!match || secretsRef.current.length === 0) return null
    const typed = match.text.slice(1)
    if (!(
      typed.startsWith("secret.") ||
      (typed && "secret.".startsWith(typed))
    ))
      return null

    return {
      from: match.from,
      to: context.pos,
      validFor: /^\{(s|se|sec|secr|secre|secret|secret\.[A-Za-z0-9_]*)$/,
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
          syntaxHighlighting(codeHighlightStyle),
          codeEditorTheme,
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
          editorChangeListener(onChangeRef),
          Prec.highest(keymap.of(codeEditorKeyBindings)),
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
    if (view) syncEditorValue(view, value)
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
                  className="text-foreground"
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
        className="w-full min-w-0 rounded-md border border-input bg-background"
      />
    </div>
  )
}
