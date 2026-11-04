import { useCallback, useEffect, useSyncExternalStore } from "react"
import type { Parser } from "@lezer/common"
import { languageOf, loaders, type Loader } from "@/lib/highlight"

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
