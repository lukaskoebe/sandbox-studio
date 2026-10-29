import type {
  Completion,
  CompletionContext,
  CompletionResult,
} from "@codemirror/autocomplete"
import type { Diagnostic } from "@codemirror/lint"
import type { Text } from "@codemirror/state"
import type { EditorView } from "@codemirror/view"

/** Names to suggest. The parser decides which of them are valid. */
export type SpecVocabulary = {
  topLevelKeys: readonly string[]
  resourceKeys: readonly string[]
  tools: readonly string[]
  sizeUnits: readonly string[]
}

/** A parser problem. Line and column are 1-based; column counts characters. */
export type SpecProblem = { line: number; column: number; message: string }

/** Common apt packages. The parser only checks package names, so this list is a convenience. */
export const aptPackageSuggestions = [
  "build-essential",
  "ca-certificates",
  "curl",
  "git",
  "jq",
  "postgresql-client",
  "redis-tools",
  "sqlite3",
  "unzip",
  "wget",
  "zip",
]

/**
 * Suggests keys, tool names, size units and apt packages for the spec at the
 * cursor. A key's context is the chain of keys above it with less indentation.
 */
export function templateSpecCompletion(
  getVocabulary: () => SpecVocabulary | undefined
): (context: CompletionContext) => CompletionResult | null {
  return (context) => {
    const vocabulary = getVocabulary()
    if (!vocabulary) return null

    const line = context.state.doc.lineAt(context.pos)
    const before = line.text.slice(0, context.pos - line.from)
    const after = line.text.slice(context.pos - line.from)

    // A unit after a size number, such as memory: 4 then GiB.
    const size =
      /^\s*(?:memory|max_memory|workspace|docker|home):\s*("?)\d+$/.exec(before)
    if (size) {
      const closeQuote = size[1] === '"' && !after.startsWith('"')
      return {
        from: context.pos,
        validFor: /^[A-Za-z]*$/,
        options: vocabulary.sizeUnits.map((unit) => ({
          label: unit,
          type: "constant",
          apply: closeQuote ? `${unit}"` : unit,
        })),
      }
    }

    // A package in the apt list.
    const item = /^(\s*)-\s+([a-z0-9+.-]*)$/.exec(before)
    if (item) {
      const parents = parentKeys(context.state.doc, line.number, item[1].length)
      if (parents.length !== 1 || parents[0] !== "apt") return null
      return {
        from: context.pos - item[2].length,
        validFor: /^[a-z0-9+.-]*$/,
        options: aptPackageSuggestions.map((name) => ({
          label: name,
          type: "text",
        })),
      }
    }

    // A mapping key.
    const key = /^(\s*)([A-Za-z_]*)$/.exec(before)
    if (!key) return null
    const parents = parentKeys(context.state.doc, line.number, key[1].length)
    const names = keyNames(vocabulary, parents)
    if (names.length === 0) return null

    const word = /^[A-Za-z_]*/.exec(after)?.[0] ?? ""
    const colonFollows = after.slice(word.length).startsWith(":")
    const isTool = parents.length === 1 && parents[0] === "tools"
    return {
      from: context.pos - key[2].length,
      to: context.pos + word.length,
      validFor: /^[A-Za-z_]*$/,
      options: names.map((name) => ({
        label: name,
        type: isTool ? "variable" : "property",
        apply: colonFollows ? name : isTool ? toolApply(name) : `${name}: `,
      })),
    }
  }
}

/** Marks each problem in `doc`, at its line and column when the parser gave one. */
export function templateSpecDiagnostics(
  doc: Text,
  problems: readonly SpecProblem[]
): Diagnostic[] {
  return problems.map(({ line: lineNumber, column, message }): Diagnostic => {
    const line = doc.line(Math.min(Math.max(lineNumber, 1), doc.lines))
    const { text } = line
    // The parser does not count a leading byte order mark as a column.
    const start = lineNumber === 1 && text.startsWith("﻿") ? 1 : 0
    if (column < 1) {
      const from = line.from + text.length - text.trimStart().length
      return {
        from,
        to: line.to,
        severity: "error",
        source: "Template spec",
        message,
      }
    }

    const from = line.from + columnOffset(text, column, start)
    const word = /^[^\s:#]*/.exec(text.slice(from - line.from))?.[0].length ?? 0
    return {
      from,
      to: Math.min(line.to, from + Math.max(word, 1)),
      severity: "error",
      source: "Template spec",
      message,
    }
  })
}

function keyNames(
  vocabulary: SpecVocabulary,
  parents: readonly string[]
): readonly string[] {
  if (parents.length === 0) return vocabulary.topLevelKeys
  if (parents.length === 1 && parents[0] === "resources") {
    return vocabulary.resourceKeys
  }
  if (parents.length === 1 && parents[0] === "tools") return vocabulary.tools
  return []
}

/** Inserts a tool name with an empty quoted version and places the cursor inside the quotes. */
function toolApply(name: string) {
  return (
    view: EditorView,
    _completion: Completion,
    from: number,
    to: number
  ) => {
    const insert = `${name}: ""`
    view.dispatch({
      changes: { from, to, insert },
      selection: { anchor: from + insert.length - 1 },
      userEvent: "input.complete",
    })
  }
}

/** The mapping keys above `lineNumber`, outermost first, that enclose an entry indented by `indent`. */
function parentKeys(doc: Text, lineNumber: number, indent: number): string[] {
  const keys: string[] = []
  let limit = indent
  for (let number = lineNumber - 1; number > 0 && limit > 0; number--) {
    const text = doc.line(number).text
    const trimmed = text.trimStart()
    if (!trimmed || trimmed.startsWith("#")) continue
    const lineIndent = text.length - trimmed.length
    if (lineIndent >= limit) continue
    const key = /^([A-Za-z_][\w-]*):\s*(?:#.*)?$/.exec(trimmed)
    if (key) keys.unshift(key[1])
    limit = lineIndent
  }
  return keys
}

// Problem columns count code points, while CodeMirror offsets count UTF-16 units.
function columnOffset(text: string, column: number, start: number) {
  let offset = start
  for (let seen = 1; seen < column && offset < text.length; seen++) {
    offset += (text.codePointAt(offset) ?? 0) > 0xffff ? 2 : 1
  }
  return offset
}
