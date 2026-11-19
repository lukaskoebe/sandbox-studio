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
 * How Studio looks, apart from light or dark: "pixel" (the default) is square with hard edges
 * and mono titles, "quiet" is soft and rounded in sans. styles.css sets pixel's tokens on :root
 * and quiet's on `data-style="quiet"`.
 */
export type StyleName = "pixel" | "quiet"

const styleKey = "studio.style"

/** The style picked in this browser; "pixel" unless quiet was picked. */
export function styleName(): StyleName {
  try {
    if (localStorage.getItem(styleKey) === "quiet") return "quiet"
  } catch {
    // Without storage the default style applies.
  }
  return "pixel"
}

/** Stores the style in this browser and applies it at once. */
export function setStyleName(style: StyleName) {
  try {
    if (style === "pixel") localStorage.removeItem(styleKey)
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
    () => "pixel"
  )
}

/**
 * How large Studio's text is in this browser, in percent of the browser's own size. The page's
 * sizes and spacing are in rem, so the whole page scales with it, as zooming would; the desktop
 * app has no zoom of its own.
 */
export const textSizes = [90, 100, 110, 125, 150] as const
export type TextSize = (typeof textSizes)[number]

const textSizeKey = "studio.text-size"

/** The text size picked in this browser; 100 unless another was picked. */
export function textSize(): TextSize {
  try {
    const stored = Number(localStorage.getItem(textSizeKey))
    const size = textSizes.find((s) => s === stored)
    if (size) return size
  } catch {
    // Without storage the default size applies.
  }
  return 100
}

/** Stores the text size in this browser and applies it at once. */
export function setTextSize(size: TextSize) {
  try {
    if (size === 100) localStorage.removeItem(textSizeKey)
    else localStorage.setItem(textSizeKey, String(size))
  } catch {
    // Without storage the choice lasts until the page reloads.
  }
  apply()
  listeners.forEach((listen) => listen())
}

/** The next text size up (1), down (-1), or back to 100 (0). */
export function stepTextSize(step: -1 | 0 | 1) {
  if (step === 0) return setTextSize(100)
  const i = textSizes.indexOf(textSize()) + step
  if (i >= 0 && i < textSizes.length) setTextSize(textSizes[i])
}

/**
 * Ctrl (⌘ on a Mac) with + or − steps the text size, and with 0 sets it back, as a browser's
 * zoom keys do: for the desktop app, which has none. Before the page's own keys, as the
 * browser's come before a page's.
 */
export function installTextSizeKeys() {
  window.addEventListener(
    "keydown",
    (e) => {
      if (!(e.ctrlKey || e.metaKey) || e.altKey) return
      const step =
        e.key === "+" || e.key === "="
          ? 1
          : e.key === "-" || e.key === "_"
            ? -1
            : e.key === "0"
              ? 0
              : undefined
      if (step === undefined) return
      e.preventDefault()
      e.stopPropagation()
      stepTextSize(step)
    },
    true
  )
}

/** The text size picked in this browser, re-rendering when it changes. */
export function useTextSize(): TextSize {
  return useSyncExternalStore(
    (listen) => {
      listeners.add(listen)
      return () => {
        listeners.delete(listen)
      }
    },
    textSize,
    () => 100
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
  const size = textSize()
  document.documentElement.style.fontSize = size === 100 ? "" : `${size}%`
}
