// Syntax highlighting for code Studio shows but doesn't edit: a file in Files and the
// lines of a diff. Lezer's parsers, as Studio's editors use, each loaded when a file of its
// language first shows up, and Lezer's tags mapped to the theme's code colors (tok-* in
// styles.css).
import { useCallback, useEffect, useSyncExternalStore } from "react"
import type { StreamParser } from "@codemirror/language"
import type { Parser } from "@lezer/common"
import { highlightCode, tagHighlighter, tags as t } from "@lezer/highlight"

/** A run of one line's text and the classes its syntax gives it, "" for none. */
export type Token = { text: string; cls: string }

const highlighter = tagHighlighter([
  { tag: t.comment, class: "tok-comment" },
  { tag: [t.keyword, t.tagName, t.heading], class: "tok-keyword" },
  { tag: [t.string, t.character, t.url], class: "tok-string" },
  {
    tag: [t.number, t.bool, t.null, t.atom, t.unit, t.color],
    class: "tok-number",
  },
  {
    tag: [t.typeName, t.className, t.namespace, t.meta, t.regexp, t.escape],
    class: "tok-type",
  },
  {
    tag: [
      t.function(t.variableName),
      t.function(t.propertyName),
      t.definition(t.function(t.variableName)),
      t.attributeName,
      t.labelName,
    ],
    class: "tok-function",
  },
  { tag: [t.operator, t.special(t.string)], class: "tok-operator" },
  { tag: t.strong, class: "font-semibold" },
  { tag: t.emphasis, class: "italic" },
  { tag: t.invalid, class: "text-destructive" },
])

type Loader = () => Promise<Parser>

/** A legacy CodeMirror mode as a parser. */
async function stream<TState>(mode: () => Promise<StreamParser<TState>>) {
  const [{ StreamLanguage }, m] = await Promise.all([
    import("@codemirror/language"),
    mode(),
  ])
  return StreamLanguage.define(m).parser
}

const loaders: Record<string, Loader> = {
  js: () =>
    import("@lezer/javascript").then((m) =>
      m.parser.configure({ dialect: "jsx" })
    ),
  ts: () =>
    import("@lezer/javascript").then((m) =>
      m.parser.configure({ dialect: "ts" })
    ),
  tsx: () =>
    import("@lezer/javascript").then((m) =>
      m.parser.configure({ dialect: "ts jsx" })
    ),
  json: () => import("@lezer/json").then((m) => m.parser),
  python: () => import("@lezer/python").then((m) => m.parser),
  go: () => import("@lezer/go").then((m) => m.parser),
  rust: () => import("@lezer/rust").then((m) => m.parser),
  java: () => import("@lezer/java").then((m) => m.parser),
  cpp: () => import("@lezer/cpp").then((m) => m.parser),
  php: () => import("@lezer/php").then((m) => m.parser),
  css: () => import("@lezer/css").then((m) => m.parser),
  html: () => import("@lezer/html").then((m) => m.parser),
  xml: () => import("@lezer/xml").then((m) => m.parser),
  markdown: () => import("@lezer/markdown").then((m) => m.parser),
  yaml: () =>
    import("@codemirror/lang-yaml").then((m) => m.yamlLanguage.parser),
  shell: () =>
    stream(() =>
      import("@codemirror/legacy-modes/mode/shell").then((m) => m.shell)
    ),
  toml: () =>
    stream(() =>
      import("@codemirror/legacy-modes/mode/toml").then((m) => m.toml)
    ),
  dockerfile: () =>
    stream(() =>
      import("@codemirror/legacy-modes/mode/dockerfile").then(
        (m) => m.dockerFile
      )
    ),
  ruby: () =>
    stream(() =>
      import("@codemirror/legacy-modes/mode/ruby").then((m) => m.ruby)
    ),
  sql: () =>
    stream(() =>
      import("@codemirror/legacy-modes/mode/sql").then((m) => m.standardSQL)
    ),
  lua: () =>
    stream(() =>
      import("@codemirror/legacy-modes/mode/lua").then((m) => m.lua)
    ),
  swift: () =>
    stream(() =>
      import("@codemirror/legacy-modes/mode/swift").then((m) => m.swift)
    ),
  kotlin: () =>
    stream(() =>
      import("@codemirror/legacy-modes/mode/clike").then((m) => m.kotlin)
    ),
  csharp: () =>
    stream(() =>
      import("@codemirror/legacy-modes/mode/clike").then((m) => m.csharp)
    ),
  scala: () =>
    stream(() =>
      import("@codemirror/legacy-modes/mode/clike").then((m) => m.scala)
    ),
  properties: () =>
    stream(() =>
      import("@codemirror/legacy-modes/mode/properties").then(
        (m) => m.properties
      )
    ),
  diff: () =>
    stream(() =>
      import("@codemirror/legacy-modes/mode/diff").then((m) => m.diff)
    ),
}

const extensions: Record<string, string> = {
  js: "js",
  mjs: "js",
  cjs: "js",
  jsx: "js",
  ts: "ts",
  mts: "ts",
  cts: "ts",
  tsx: "tsx",
  json: "json",
  jsonc: "json",
  json5: "json",
  lock: "json",
  py: "python",
  pyi: "python",
  go: "go",
  rs: "rust",
  java: "java",
  c: "cpp",
  h: "cpp",
  cc: "cpp",
  cpp: "cpp",
  cxx: "cpp",
  hpp: "cpp",
  hh: "cpp",
  m: "cpp",
  php: "php",
  css: "css",
  scss: "css",
  less: "css",
  html: "html",
  htm: "html",
  vue: "html",
  svelte: "html",
  xml: "xml",
  svg: "xml",
  plist: "xml",
  xsd: "xml",
  md: "markdown",
  markdown: "markdown",
  mdx: "markdown",
  yaml: "yaml",
  yml: "yaml",
  sh: "shell",
  bash: "shell",
  zsh: "shell",
  fish: "shell",
  toml: "toml",
  rb: "ruby",
  rake: "ruby",
  gemspec: "ruby",
  sql: "sql",
  lua: "lua",
  swift: "swift",
  kt: "kotlin",
  kts: "kotlin",
  cs: "csharp",
  scala: "scala",
  sc: "scala",
  ini: "properties",
  cfg: "properties",
  conf: "properties",
  properties: "properties",
  env: "properties",
  diff: "diff",
  patch: "diff",
}

const names: Record<string, string> = {
  dockerfile: "dockerfile",
  containerfile: "dockerfile",
  ".bashrc": "shell",
  ".zshrc": "shell",
  ".profile": "shell",
  ".bash_profile": "shell",
  ".env": "properties",
  ".gitconfig": "properties",
  ".editorconfig": "properties",
  gemfile: "ruby",
  rakefile: "ruby",
  "go.mod": "go",
  "go.sum": "properties",
}

/** The language of a file by its name, or undefined for one shown plain. */
export function languageOf(path: string) {
  const name = (path.split("/").at(-1) ?? path).toLowerCase()
  if (names[name]) return names[name]
  if (name.startsWith("dockerfile.") || name.endsWith(".dockerfile"))
    return "dockerfile"
  if (name.startsWith(".env.")) return "properties"
  const dot = name.lastIndexOf(".")
  return dot > 0 ? extensions[name.slice(dot + 1)] : undefined
}

const parsers = new Map<string, Parser>()
const pending = new Set<string>()
const listeners = new Set<() => void>()
let version = 0

function load(lang: string) {
  const loader = loaders[lang] as Loader | undefined
  if (parsers.has(lang) || pending.has(lang) || !loader) return
  pending.add(lang)
  loader()
    .then((p) => {
      parsers.set(lang, p)
      version++
      listeners.forEach((l) => l())
    })
    // A language that fails to load is shown plain.
    .catch(() => {})
}

function subscribe(listener: () => void) {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

/**
 * The parser for each of these files once its language has loaded: undefined meanwhile, and
 * for a file of no language Studio knows.
 */
export function useParsers(paths: string[]) {
  const v = useSyncExternalStore(subscribe, () => version)
  const key = paths.join("\n")
  useEffect(() => {
    for (const p of key.split("\n")) {
      const lang = languageOf(p)
      if (lang) load(lang)
    }
  }, [key])
  // v changes when a parser loads, and with it the function.
  return useCallback((path: string) => parsers.get(languageOf(path) ?? ""), [v])
}

/** Past this, a file shows plain: highlighting it would hold up the page. */
const maxLength = 256 * 1024

/** The text's lines, each as tokens; plain without a parser. */
export function highlightLines(text: string, parser?: Parser): Token[][] {
  if (!parser || text.length > maxLength)
    return text.split("\n").map((l) => [{ text: l, cls: "" }])
  const lines: Token[][] = [[]]
  try {
    highlightCode(
      text,
      parser.parse(text),
      highlighter,
      (s, cls) => lines.at(-1)!.push({ text: s, cls }),
      () => lines.push([])
    )
  } catch {
    return text.split("\n").map((l) => [{ text: l, cls: "" }])
  }
  return lines
}
