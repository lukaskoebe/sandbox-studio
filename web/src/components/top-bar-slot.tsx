import { useSyncExternalStore, type ReactNode } from "react"
import { createPortal } from "react-dom"

/**
 * Places in the breadcrumb row for what belongs to the page below it: "status" right after the
 * crumbs, such as what a teammate is doing, and "controls" at the row's right end, such as a
 * teammate's layout switch. The row renders each TopBarSlot once; a page puts its parts there
 * with InTopBar, and they go when the page does.
 */
type SlotName = "status" | "controls"

const slots: Record<SlotName, HTMLElement | null> = {
  status: null,
  controls: null,
}
const listeners = new Set<() => void>()

const subscribe = (l: () => void) => {
  listeners.add(l)
  return () => void listeners.delete(l)
}

const setSlot = (name: SlotName) => (el: HTMLElement | null) => {
  if (slots[name] === el) return
  slots[name] = el
  listeners.forEach((l) => l())
}
// Stable refs, so React doesn't detach and reattach a slot on every render.
const refs: Record<SlotName, (el: HTMLElement | null) => void> = {
  status: setSlot("status"),
  controls: setSlot("controls"),
}

/** The breadcrumb row's place for a page's controls, or (in its list) for its status. */
export function TopBarSlot({ name = "controls" }: { name?: SlotName }) {
  return name === "status" ? (
    <li
      ref={refs.status}
      className="ml-1.5 flex min-w-0 shrink items-center gap-1.5 empty:hidden"
    />
  ) : (
    <div
      ref={refs.controls}
      className="flex shrink-0 items-center empty:hidden"
    />
  )
}

/** Shows children in one of the breadcrumb row's places while the calling page is mounted. */
export function InTopBar({
  slot = "controls",
  children,
}: {
  slot?: SlotName
  children: ReactNode
}) {
  const el = useSyncExternalStore(subscribe, () => slots[slot])
  return el ? createPortal(children, el) : null
}
