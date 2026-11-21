import { useSyncExternalStore, type ReactNode } from "react"
import { createPortal } from "react-dom"

/**
 * A place at the right end of the breadcrumb row for what belongs to the page below it, such as
 * a teammate's layout switch. The row renders TopBarSlot once; a page puts its controls there
 * with InTopBar, and they go when the page does.
 */
let slot: HTMLElement | null = null
const listeners = new Set<() => void>()

const subscribe = (l: () => void) => {
  listeners.add(l)
  return () => void listeners.delete(l)
}

const setSlot = (el: HTMLElement | null) => {
  if (slot === el) return
  slot = el
  listeners.forEach((l) => l())
}

/** The breadcrumb row's place for a page's controls. */
export function TopBarSlot() {
  return (
    <div ref={setSlot} className="flex shrink-0 items-center empty:hidden" />
  )
}

/** Shows children in the breadcrumb row's place while the calling page is mounted. */
export function InTopBar({ children }: { children: ReactNode }) {
  const el = useSyncExternalStore(subscribe, () => slot)
  return el ? createPortal(children, el) : null
}
