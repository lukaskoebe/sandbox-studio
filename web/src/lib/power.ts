import { useSyncExternalStore } from "react"
import { quitApp } from "@/lib/desktop"

/**
 * Restarting and quitting Studio, which the Studio menu and the command palette both ask for:
 * one dialog, outside the sidebar (a phone's drawer is not there while it is shut), asks and
 * does it (StudioPower in components/studio-menu.tsx).
 */
export type PowerAction = "restart" | "quit"

type State = { asking: PowerAction | null; stopped: boolean }

let state: State = { asking: null, stopped: false }
const listeners = new Set<() => void>()

function set(next: Partial<State>) {
  state = { ...state, ...next }
  for (const l of listeners) l()
}

function subscribe(l: () => void) {
  listeners.add(l)
  return () => listeners.delete(l)
}

/** Opens the dialog that asks whether to restart or quit Studio. */
export function askPower(action: PowerAction) {
  set({ asking: action })
}

/** Closes that dialog. */
export function closePower() {
  set({ asking: null })
}

/**
 * Studio quit, from this page or anywhere else (`studio quit`, the tray, another tab): the
 * page says so, and the Studio app closes with it.
 */
export function studioQuit() {
  if (state.stopped) return
  set({ asking: null, stopped: true })
  quitApp()
}

export function usePower(): State {
  return useSyncExternalStore(subscribe, () => state)
}
