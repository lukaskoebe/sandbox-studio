import { useSyncExternalStore } from "react"

/** What the viewer picked. "system" follows the OS light or dark preference. */
export type ThemeMode = "system" | "light" | "dark"

const key = "studio.theme"
const systemDark = "(prefers-color-scheme: dark)"
const listeners = new Set<() => void>()

/** The mode picked in this browser; "system" unless light or dark was picked. */
export function themeMode(): ThemeMode {
  try {
    const stored = localStorage.getItem(key)
    if (stored === "light" || stored === "dark") return stored
  } catch {
    // Storage can be unavailable (private windows); the system theme still applies.
  }
  return "system"
}

/** Stores the mode in this browser and applies it at once. */
export function setThemeMode(mode: ThemeMode) {
  try {
    if (mode === "system") localStorage.removeItem(key)
    else localStorage.setItem(key, mode)
  } catch {
    // Without storage the choice lasts until the page reloads.
  }
  apply()
  listeners.forEach((listen) => listen())
}

/** The mode picked in this browser, re-rendering when it changes. */
export function useThemeMode(): ThemeMode {
  return useSyncExternalStore(
    (listen) => {
      listeners.add(listen)
      return () => {
        listeners.delete(listen)
      }
    },
    themeMode,
    () => "system"
  )
}

/** Applies the stored mode and keeps following the OS preference. Call once at startup. */
export function initTheme() {
  apply()
  window.matchMedia(systemDark).addEventListener("change", apply)
}

function apply() {
  const mode = themeMode()
  const dark =
    mode === "dark" ||
    (mode === "system" && window.matchMedia(systemDark).matches)
  document.documentElement.classList.toggle("dark", dark)
}
