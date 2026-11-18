import {
  type ReactNode,
  type RefObject,
  useCallback,
  useEffect,
  useState,
} from "react"
import { CaretRightIcon } from "@phosphor-icons/react"
import { cn } from "@/lib/utils"

const foldsKey = "studio.changes-folds"

type Folds = Record<string, Record<string, boolean>>

function readFolds(): Folds {
  try {
    const parsed: unknown = JSON.parse(localStorage.getItem(foldsKey) ?? "{}")
    return parsed && typeof parsed === "object" ? (parsed as Folds) : {}
  } catch {
    return {}
  }
}

/**
 * Which parts of a computer's Changes rail are open, kept in this browser per computer: its
 * sections, by name, and its repositories, by path. A part not yet folded or unfolded is open,
 * except a repository after the first.
 */
export function useRailFolds(sandboxId: string) {
  const [folds, setFolds] = useState(() => readFolds()[sandboxId] ?? {})
  useEffect(() => setFolds(readFolds()[sandboxId] ?? {}), [sandboxId])
  const set = useCallback(
    (key: string, open: boolean) =>
      setFolds((now) => {
        const next = { ...now, [key]: open }
        try {
          localStorage.setItem(
            foldsKey,
            JSON.stringify({ ...readFolds(), [sandboxId]: next })
          )
        } catch {
          // Private windows and blocked storage only forget the folds.
        }
        return next
      }),
    [sandboxId]
  )
  const setSection = useCallback(
    (id: string, open: boolean) => set(`section:${id}`, open),
    [set]
  )
  return {
    section: (id: string) => folds[`section:${id}`] ?? true,
    setSection,
    repo: (path: string, index: number) => folds[`repo:${path}`] ?? index === 0,
    setRepo: (path: string, open: boolean) => set(`repo:${path}`, open),
  }
}

/** A part of the Changes rail that folds: a header with its count, then what it holds. */
export function RailSection({
  label,
  count,
  open,
  onOpenChange,
  children,
}: {
  label: string
  count?: ReactNode
  open: boolean
  onOpenChange: (open: boolean) => void
  children: ReactNode
}) {
  return (
    <section aria-label={label} className="min-w-0 border-b">
      <h3>
        <button
          type="button"
          data-rail-section
          aria-expanded={open}
          onClick={() => onOpenChange(!open)}
          className="flex min-h-8 w-full min-w-0 items-center gap-1.5 px-3 text-left text-caption font-medium tracking-wide text-muted-foreground uppercase outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring/30 focus-visible:ring-inset"
        >
          <CaretRightIcon
            className={cn(
              "size-3 shrink-0 transition-transform",
              open && "rotate-90"
            )}
          />
          <span className="min-w-0 flex-1 truncate">{label}</span>
          {count !== undefined && (
            <span className="shrink-0 font-mono text-meta tracking-normal normal-case">
              {count}
            </span>
          )}
        </button>
      </h3>
      {open && <div className="min-w-0 pb-1.5">{children}</div>}
    </section>
  )
}

/**
 * [ and ] move between the rail's section headers, as long as no field, terminal or dialog has
 * the keys.
 */
export function useSectionKeys(rail: RefObject<HTMLElement | null>) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "[" && e.key !== "]") return
      if (e.metaKey || e.ctrlKey || e.altKey || e.defaultPrevented) return
      const target = e.target instanceof HTMLElement ? e.target : null
      if (
        target?.closest(
          "input, textarea, select, [contenteditable=''], [contenteditable='true'], .xterm, [role='dialog'], [role='alertdialog'], [role='menu']"
        )
      )
        return
      const headers = [
        ...(rail.current?.querySelectorAll<HTMLButtonElement>(
          "button[data-rail-section]"
        ) ?? []),
      ].filter((h) => h.offsetParent !== null)
      if (headers.length === 0) return
      const at = headers.findIndex((h) =>
        h.closest("section")?.contains(document.activeElement)
      )
      const step = e.key === "]" ? 1 : -1
      const next =
        at < 0
          ? headers[step > 0 ? 0 : headers.length - 1]
          : headers[Math.max(0, Math.min(headers.length - 1, at + step))]
      next.focus()
      next.scrollIntoView({ block: "nearest" })
      e.preventDefault()
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [rail])
}
