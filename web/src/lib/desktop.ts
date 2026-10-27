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
 * What the app said it does for the page, from the studio-app parameter of the login link it
 * opened: "notify" when the system shows the page's notifications. It is kept for the
 * session, as the page moves on without it.
 */
function appFeatures(): string {
  if (typeof window === "undefined") return ""
  const url = new URL(window.location.href)
  const given = url.searchParams.get("studio-app")
  try {
    if (given !== null) {
      sessionStorage.setItem("studio.app", given)
      url.searchParams.delete("studio-app")
      history.replaceState(history.state, "", url.href)
      return given
    }
    return sessionStorage.getItem("studio.app") ?? ""
  } catch {
    return given ?? ""
  }
}

/** Whether the page runs in the Studio app rather than a browser. */
export const inApp = handler() !== undefined

/** Whether the app shows notifications through the system; else the page keeps its own. */
export const appNotifies = inApp && appFeatures() === "notify"

function post(kind: "open" | "notify", payload: string) {
  handler()?.postMessage(`studio:${kind} ${payload}`)
}

/** Opens an http(s) address in the system browser; the app opens nothing else. */
export function openExternal(url: string) {
  post("open", new URL(url, window.location.href).href)
}

/** Shows a notification of the system's; clicking it brings the app's window to the front. */
export function notifyApp(n: { id: string; title: string; body: string }) {
  post("notify", JSON.stringify(n))
}

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
