import {
  createContext,
  useContext,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from "react"
import { useBlocker } from "@tanstack/react-router"
import { CheckIcon } from "@phosphor-icons/react"
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

/** How long typing pauses before a change saves. */
const pause = 1000

/** One form's share of the page's changes. */
type Part = {
  dirty: boolean
  valid: boolean
  /** What a save would send: a change of it is a new change to save. */
  signature: string
  /** Saves the form; it reports its own errors and rejects when it fails. */
  save: () => Promise<unknown>
}

type State = { dirty: boolean; valid: boolean; signature: string }

const AutoSaveContext = createContext<
  | {
      set: (id: string, state: State | undefined) => void
      parts: React.RefObject<Map<string, Part>>
    }
  | undefined
>(undefined)

/**
 * Registers a form with the page's AutoSave, which saves its changes as they are made. Outside
 * one it returns false, and the form keeps its own Save.
 */
export function useSavePart(id: string, part: Part): boolean {
  const bar = useContext(AutoSaveContext)
  // The functions change every render; AutoSave calls the latest.
  bar?.parts.current.set(id, part)
  const { dirty, valid, signature } = part
  useEffect(() => {
    bar?.set(id, { dirty, valid, signature })
  }, [bar, id, dirty, valid, signature])
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
 * Saves the changes of a page's forms as they are made, as Lead's settings do: a change saves
 * once typing pauses, and at once when the page is left. A change that can't be saved yet, such
 * as an empty name, waits, and leaving with one asks first. A failed save says why and is tried
 * again on the next change. Switches and selects that save as they change stay as they are.
 */
export function AutoSave({
  children,
  leaves,
}: {
  children: ReactNode
  /** Whether a change of the search alone leaves the forms too, such as closing their sheet. */
  leaves?: (search: Record<string, unknown>) => boolean
}) {
  const [states, setStates] = useState<Record<string, State>>({})
  const parts = useRef(new Map<string, Part>())
  // The signature each part last sent, so a failed save isn't sent again unchanged.
  const sent = useRef(new Map<string, string>())
  const [saving, setSaving] = useState(0)
  const [saved, setSaved] = useState(false)
  const set = useRef((id: string, state: State | undefined) =>
    setStates((prev) => {
      const next = { ...prev }
      if (state) next[id] = state
      else delete next[id]
      return next
    })
  ).current
  const [context] = useState(() => ({ set, parts }))

  const pending = Object.entries(states).filter(
    ([id, s]) => s.dirty && s.valid && sent.current.get(id) !== s.signature
  )
  const incomplete = Object.values(states).some((s) => s.dirty && !s.valid)
  const pendingRef = useRef(pending)
  pendingRef.current = pending

  const saveNow = () =>
    Promise.allSettled(
      pendingRef.current.map(async ([id, s]) => {
        const part = parts.current.get(id)
        if (!part) return
        sent.current.set(id, s.signature)
        setSaving((n) => n + 1)
        setSaved(false)
        try {
          await part.save()
          setSaved(true)
        } finally {
          setSaving((n) => n - 1)
        }
      })
    )

  const key = pending.map(([id, s]) => `${id}\0${s.signature}`).join("\n")
  useEffect(() => {
    if (!key) return
    const t = setTimeout(() => void saveNow(), pause)
    return () => clearTimeout(t)
    // saveNow reads the latest changes when it runs.
  }, [key])
  useEffect(() => {
    if (!saved) return
    const t = setTimeout(() => setSaved(false), 2500)
    return () => clearTimeout(t)
  }, [saved])

  const blocker = useBlocker({
    // Opening a section changes only the search, which keeps the changes.
    shouldBlockFn: async ({ current, next }) => {
      const leaving =
        current.pathname !== next.pathname || !!leaves?.(next.search)
      if (!leaving) return false
      if (pendingRef.current.length > 0) await saveNow()
      return incomplete
    },
    enableBeforeUnload: () => incomplete || pending.length > 0 || saving > 0,
    withResolver: true,
  })

  return (
    <AutoSaveContext.Provider value={context}>
      {children}
      <div
        role="status"
        aria-live="polite"
        className="sticky bottom-0 flex min-h-8 items-center justify-end gap-1.5 py-2 text-xs text-muted-foreground"
      >
        {incomplete ? (
          "Not saved until the form is complete"
        ) : saving > 0 || pending.length > 0 ? (
          <>
            <Spinner className="size-3" />
            Saving…
          </>
        ) : saved ? (
          <>
            <CheckIcon className="size-3" />
            Saved
          </>
        ) : null}
      </div>
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
              A change on this page isn't complete, so it can't be saved. It is
              lost if you leave.
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
    </AutoSaveContext.Provider>
  )
}
