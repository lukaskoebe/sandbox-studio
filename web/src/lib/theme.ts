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

/**
 * How Studio looks, apart from light or dark: "quiet" is soft and rounded, "pixel" is square
 * with hard edges, like the week on Home. styles.css keys its tokens on `data-style`.
 */
export type StyleName = "quiet" | "pixel"

const styleKey = "studio.style"

/** The style picked in this browser; "quiet" unless pixel was picked. */
export function styleName(): StyleName {
  try {
    if (localStorage.getItem(styleKey) === "pixel") return "pixel"
  } catch {
    // Without storage the default style applies.
  }
  return "quiet"
}

/** Stores the style in this browser and applies it at once. */
export function setStyleName(style: StyleName) {
  try {
    if (style === "quiet") localStorage.removeItem(styleKey)
    else localStorage.setItem(styleKey, style)
  } catch {
    // Without storage the default style stays.
  }
  apply()
  listeners.forEach((listen) => listen())
}

/** The style picked in this browser, re-rendering when it changes. */
export function useStyleName(): StyleName {
  return useSyncExternalStore(
    (listen) => {
      listeners.add(listen)
      return () => {
        listeners.delete(listen)
      }
    },
    styleName,
    () => "quiet"
  )
}

/** Applies the stored mode and keeps following the OS preference. Call once at startup. */
export function initTheme() {
  apply()
  window.matchMedia(systemDark).addEventListener("change", () => {
    apply()
    listeners.forEach((listen) => listen())
  })
}

/** Whether the page shows dark now: the mode picked, or the OS preference under "system". */
function resolvedDark() {
  return document.documentElement.classList.contains("dark")
}

/** "light" or "dark", whichever the page shows, re-rendering when it changes. */
export function useResolvedTheme(): "light" | "dark" {
  return useSyncExternalStore(
    (listen) => {
      listeners.add(listen)
      return () => {
        listeners.delete(listen)
      }
    },
    () => (resolvedDark() ? "dark" : "light"),
    () => "dark"
  )
}

const terminalKey = "studio.terminal-theme"

/**
 * Whether terminals stay dark in the light theme, as picked in this browser. Several viewers
 * can watch one tmux session, and a program in it that picked its colors for a dark
 * background stays readable this way.
 */
function terminalsDark() {
  try {
    return localStorage.getItem(terminalKey) === "dark"
  } catch {
    return false
  }
}

/** Keeps terminals dark in the light theme, or lets them follow it again. */
export function setTerminalsDark(dark: boolean) {
  try {
    if (dark) localStorage.setItem(terminalKey, "dark")
    else localStorage.removeItem(terminalKey)
  } catch {
    // Without storage the choice lasts until the page reloads.
  }
  listeners.forEach((listen) => listen())
}

/** "light" or "dark", the palette terminals use now, re-rendering when it changes. */
export function useTerminalTheme(): "light" | "dark" {
  const page = useResolvedTheme()
  const dark = useSyncExternalStore(
    (listen) => {
      listeners.add(listen)
      return () => {
        listeners.delete(listen)
      }
    },
    terminalsDark,
    () => false
  )
  return dark ? "dark" : page
}

/** Whether terminals are kept dark in this browser, re-rendering when it changes. */
export function useTerminalsDark(): boolean {
  return useSyncExternalStore(
    (listen) => {
      listeners.add(listen)
      return () => {
        listeners.delete(listen)
      }
    },
    terminalsDark,
    () => false
  )
}

function apply() {
  const mode = themeMode()
  const dark =
    mode === "dark" ||
    (mode === "system" && window.matchMedia(systemDark).matches)
  document.documentElement.classList.toggle("dark", dark)
  document.documentElement.dataset.style = styleName()
}
