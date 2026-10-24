import {
  createContext,
  useContext,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from "react"
import { useBlocker } from "@tanstack/react-router"
import { Button } from "@/components/ui/button"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { Spinner } from "@/components/ui/spinner"

/** One form's share of the page's unsaved changes. */
type Part = {
  dirty: boolean
  valid: boolean
  /** Saves the form; it reports its own errors and rejects when it fails. */
  save: () => Promise<unknown>
  discard: () => void
}

type State = { dirty: boolean; valid: boolean }

const SaveBarContext = createContext<
  | {
      set: (id: string, state: State | undefined) => void
      parts: React.RefObject<Map<string, Part>>
    }
  | undefined
>(undefined)

/**
 * Registers a form with the page's save bar, which shows while it has changes. Outside a
 * SaveBar it returns false, and the form keeps its own Save.
 */
export function useSavePart(id: string, part: Part): boolean {
  const bar = useContext(SaveBarContext)
  // The functions change every render; the bar calls the latest.
  bar?.parts.current.set(id, part)
  const { dirty, valid } = part
  useEffect(() => {
    bar?.set(id, { dirty, valid })
  }, [bar, id, dirty, valid])
  useEffect(
    () => () => {
      bar?.parts.current.delete(id)
      bar?.set(id, undefined)
    },
    [bar, id]
  )
  return bar !== undefined
}

/**
 * One "Unsaved changes · Discard · Save" bar for the forms of a page, shown only while one of
 * them has changes. Save saves each changed form; leaving the page with changes asks first.
 * Switches and selects that save as they change stay as they are.
 */
export function SaveBar({
  children,
  leaves,
}: {
  children: ReactNode
  /** Whether a change of the search alone leaves the forms too, such as closing their sheet. */
  leaves?: (search: Record<string, unknown>) => boolean
}) {
  const [states, setStates] = useState<Record<string, State>>({})
  const parts = useRef(new Map<string, Part>())
  const [saving, setSaving] = useState(false)
  const set = useRef((id: string, state: State | undefined) =>
    setStates((prev) => {
      const next = { ...prev }
      if (state) next[id] = state
      else delete next[id]
      return next
    })
  ).current
  const [context] = useState(() => ({ set, parts }))
  const changed = Object.entries(states).filter(([, s]) => s.dirty)
  const dirty = changed.length > 0
  const valid = changed.every(([, s]) => s.valid)

  const blocker = useBlocker({
    // Opening a section changes only the search, which keeps the changes.
    shouldBlockFn: ({ current, next }) =>
      dirty &&
      (current.pathname !== next.pathname ||
        !!leaves?.(next.search as Record<string, unknown>)),
    enableBeforeUnload: () => dirty,
    withResolver: true,
  })

  const save = async () => {
    setSaving(true)
    try {
      await Promise.allSettled(
        changed.map(([id]) => parts.current.get(id)?.save())
      )
    } finally {
      setSaving(false)
    }
  }
  const discard = () => {
    for (const [id] of changed) parts.current.get(id)?.discard()
  }

  return (
    <SaveBarContext.Provider value={context}>
      {children}
      {dirty && (
        <div
          role="region"
          aria-label="Unsaved changes"
          className="sticky bottom-0 z-10 flex items-center gap-2 rounded-lg border bg-background/95 px-3 py-2 shadow-md backdrop-blur"
        >
          <span className="min-w-0 flex-1 text-xs text-muted-foreground">
            Unsaved changes
          </span>
          <Button variant="ghost" size="sm" disabled={saving} onClick={discard}>
            Discard
          </Button>
          <Button size="sm" disabled={!valid || saving} onClick={save}>
            {saving && <Spinner />}
            Save
          </Button>
        </div>
      )}
      <AlertDialog
        open={blocker.status === "blocked"}
        onOpenChange={(open) => {
          if (!open && blocker.status === "blocked") blocker.reset()
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Leave without saving?</AlertDialogTitle>
            <AlertDialogDescription>
              The changes on this page are lost.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Stay</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              onClick={() => blocker.proceed?.()}
            >
              Leave
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </SaveBarContext.Provider>
  )
}
