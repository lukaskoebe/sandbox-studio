/**
 * The Studio app (cmd/studio-app) shows this UI in a webview of its own, which opens no new
 * windows and may have no web notifications. The page talks to the app through the webview's
 * message handler instead: WebKit's on Linux and macOS, WebView2's on Windows. The app checks
 * that a message comes from Studio's own origin. In a browser none of this runs.
 */

type Poster = { postMessage: (message: string) => void }

declare global {
  interface Window {
    webkit?: { messageHandlers?: { external?: Poster } }
    chrome?: { webview?: Poster }
  }
}

/** The app's message handler, when this page runs in the app. */
function handler(): Poster | undefined {
  if (typeof window === "undefined") return undefined
  return window.webkit?.messageHandlers?.external ?? window.chrome?.webview
}

/**
 * What the app said about itself in a parameter of the login link it opened. It is kept for
 * the session, as the page moves on without it.
 */
function fromApp(param: string, key: string): string {
  if (typeof window === "undefined") return ""
  const url = new URL(window.location.href)
  const given = url.searchParams.get(param)
  try {
    if (given !== null) {
      sessionStorage.setItem(key, given)
      url.searchParams.delete(param)
      history.replaceState(history.state, "", url.href)
      return given
    }
    return sessionStorage.getItem(key) ?? ""
  } catch {
    return given ?? ""
  }
}

/** Whether the page runs in the Studio app rather than a browser. */
export const inApp = handler() !== undefined

/** Whether the app shows notifications through the system; else the page keeps its own. */
export const appNotifies =
  inApp && fromApp("studio-app", "studio.app") === "notify"

/**
 * How the app's window is framed, when the page draws part of it: "frameless" for no frame at
 * all (Linux), "inset" for the system's window buttons over the page (macOS).
 */
export const appWindow = inApp ? fromApp("studio-window", "studio.window") : ""

function post(kind: "open" | "notify" | "window", payload: string) {
  handler()?.postMessage(`studio:${kind} ${payload}`)
}

export type WindowAction = "minimise" | "maximise" | "close"

/** Does what a frameless window's buttons do. */
export function windowAction(action: WindowAction) {
  post("window", action)
}

/**
 * Starts moving the window with the pointer. Wails takes these two messages itself; its
 * runtime isn't served on Studio's address, so the page sends them as that would.
 */
export function windowDrag() {
  handler()?.postMessage("wails:drag")
}

export type WindowEdge = "n" | "ne" | "e" | "se" | "s" | "sw" | "w" | "nw"

/** Starts resizing the window from an edge or a corner with the pointer. */
export function windowResize(edge: WindowEdge) {
  handler()?.postMessage(`wails:resize:${edge}-resize`)
}

/** Closes the app, after Studio shut down from the page. */
export function quitApp() {
  handler()?.postMessage("studio:quit")
}

/** Opens an http(s) address in the system browser; the app opens nothing else. */
export function openExternal(url: string) {
  post("open", new URL(url, window.location.href).href)
}

/**
 * Shows a notification of the system's; clicking it brings the app's window to the front. With
 * allowAll it also has an Allow all button, which fires appAllowAllEvent on the page.
 */
export function notifyApp(n: {
  id: string
  title: string
  body: string
  allowAll?: boolean
}) {
  post("notify", JSON.stringify(n))
}

/** Fired on the window when a notification's Allow all is clicked; its detail is the notice's ID. */
export const appAllowAllEvent = "studio:allow-all"

/**
 * Sends what would open a new window to the system browser: window.open, links with a target
 * and links to other origins. Links within Studio stay in the app's window.
 */
export function installAppLinks() {
  if (!inApp) return
  window.open = (url?: string | URL) => {
    if (url && String(url) !== "about:blank") openExternal(String(url))
    return null
  }
  document.addEventListener(
    "click",
    (e) => {
      if (e.defaultPrevented || !(e.target instanceof Element)) return
      const a = e.target.closest<HTMLAnchorElement>("a[href]")
      if (!a || a.hasAttribute("download")) return
      const url = new URL(a.href, window.location.href)
      if (url.protocol !== "http:" && url.protocol !== "https:") return
      if (a.target === "_blank" || url.origin !== window.location.origin) {
        e.preventDefault()
        openExternal(url.href)
      }
    },
    true
  )
}
