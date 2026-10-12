// Shared look and behaviour of Studio's CodeMirror editors (Caddyfile, template spec).
import { closeCompletion, completionKeymap } from "@codemirror/autocomplete"
import {
  defaultKeymap,
  historyKeymap,
  indentWithTab,
} from "@codemirror/commands"
import { HighlightStyle } from "@codemirror/language"
import { Annotation, Transaction } from "@codemirror/state"
import { EditorView } from "@codemirror/view"
import { tags } from "@lezer/highlight"

export const codeHighlightStyle = HighlightStyle.define([
  { tag: tags.comment, color: "var(--muted-foreground)" },
  { tag: tags.keyword, color: "var(--code-keyword)", fontWeight: "600" },
  { tag: tags.propertyName, color: "var(--code-keyword)" },
  { tag: tags.meta, color: "var(--code-meta)", fontWeight: "600" },
  { tag: tags.string, color: "var(--code-string)" },
  { tag: tags.special(tags.string), color: "var(--code-meta)" },
  {
    tag: tags.special(tags.variableName),
    color: "var(--code-variable)",
    fontWeight: "500",
  },
  { tag: [tags.labelName, tags.typeName], color: "var(--code-operator)" },
  { tag: tags.operator, color: "var(--code-operator)" },
  { tag: tags.punctuation, color: "var(--muted-foreground)" },
])

const monospace =
  "ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace"

export const codeEditorTheme = EditorView.theme({
  "&": {
    borderRadius: "inherit",
    backgroundColor: "var(--background)",
    color: "var(--foreground)",
    fontSize: "0.75rem",
  },
  "&.cm-focused": {
    outline: "2px solid color-mix(in oklch, var(--ring) 35%, transparent)",
    outlineOffset: "-2px",
  },
  // No ScrollArea: CodeMirror measures and scrolls its own scroller.
  ".cm-scroller": {
    borderRadius: "inherit",
    maxHeight: "24rem",
    overflow: "auto",
    fontFamily: monospace,
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
    maxWidth: "min(24rem, calc(100vw - 2rem))",
  },
  ".cm-tooltip-autocomplete ul": { fontFamily: monospace },
  ".cm-tooltip-autocomplete ul li[aria-selected]": {
    backgroundColor: "var(--accent)",
    color: "var(--accent-foreground)",
  },
  ".cm-completionDetail": {
    color: "var(--muted-foreground)",
    fontStyle: "normal",
  },
})

export const codeEditorKeyBindings = [
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

/** Marks transactions that mirror a new `value` prop into the editor. */
export const externalValueSync = Annotation.define<boolean>()

/** Replaces the changed middle of the document with `value`, outside the undo history. */
export function syncEditorValue(view: EditorView, value: string) {
  const previous = view.state.doc.toString()
  if (previous === value) return

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
}

/** Calls `onChange` with the document after edits that did not come from `syncEditorValue`. */
export function editorChangeListener(onChangeRef: {
  current: (value: string) => void
}) {
  return EditorView.updateListener.of((update) => {
    if (!update.docChanged) return

    const isExternalSync = update.transactions.some((transaction) =>
      transaction.annotation(externalValueSync)
    )
    if (!isExternalSync) onChangeRef.current(update.state.doc.toString())
  })
}
