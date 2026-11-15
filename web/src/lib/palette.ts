import { useSyncExternalStore } from "react"

// Whether the search palette (components/command-palette.tsx) is open. The sidebar's Search row,
// the breadcrumb's search button and ⌘K open it from anywhere.
let open = false
const listeners = new Set<() => void>()

export function setPaletteOpen(next: boolean) {
  if (open === next) return
  open = next
  for (const l of listeners) l()
}

export const openPalette = () => setPaletteOpen(true)

export function usePaletteOpen() {
  return useSyncExternalStore(
    (onChange) => {
      listeners.add(onChange)
      return () => listeners.delete(onChange)
    },
    () => open
  )
}
