import { useState } from "react"
import {
  ArrowSquareOutIcon,
  CheckIcon,
  CopyIcon,
  WrenchIcon,
  XIcon,
} from "@phosphor-icons/react"
import { Button } from "@/components/ui/button"
import type { components } from "@/lib/api/schema.gen"

export type OpenedDevTools = components["schemas"]["BrowserDevTools"]

/**
 * Whether this browser runs Chrome's DevTools, whose frontend comes from the teammate's
 * Chromium and runs in Chromium only: Chrome, Edge, Brave, the Windows app's WebView2. Not
 * Firefox, Safari or the app's WebKit on macOS and Linux.
 */
export const runsDevTools = (() => {
  const data = (
    navigator as Navigator & {
      userAgentData?: { brands?: { brand: string }[] }
    }
  ).userAgentData
  return !!data?.brands?.some((b) => b.brand === "Chromium")
})()

/**
 * Opens DevTools in a window of their own; the app sends it to the system browser. One window
 * per teammate's session: opening again brings it back with the page.
 */
export function popOutDevTools(url: string, name: string) {
  window.open(url, name, "popup,width=1200,height=800")
}

/**
 * Chrome's DevTools for the browser's page, docked under it (docs/browser.md). The frame
 * is DevTools' own origin; the header names the page and breaks DevTools out into a window.
 */
export function DevToolsPanel({
  devtools,
  name,
  height,
  onPopOut,
  onClose,
}: {
  devtools: OpenedDevTools
  /** The teammate whose browser it is. */
  name: string
  height: number
  onPopOut: () => void
  onClose: () => void
}) {
  const page = devtools.title || devtools.pageUrl || "the page"
  return (
    <section
      aria-label="DevTools"
      className="flex shrink-0 flex-col border-t bg-background"
      style={{ height }}
    >
      <div className="flex h-8 shrink-0 items-center gap-1.5 border-b px-2 text-xs">
        <WrenchIcon className="size-3.5 shrink-0 text-muted-foreground" />
        <span className="font-medium">DevTools</span>
        <span
          className="min-w-0 flex-1 truncate text-muted-foreground"
          title={devtools.pageUrl}
        >
          {page}
        </span>
        <Button
          size="icon-sm"
          variant="ghost"
          aria-label="Open DevTools in a window"
          title="Open in a window of their own"
          onClick={onPopOut}
        >
          <ArrowSquareOutIcon />
        </Button>
        <Button
          size="icon-sm"
          variant="ghost"
          aria-label="Close DevTools"
          title="Close DevTools"
          onClick={onClose}
        >
          <XIcon />
        </Button>
      </div>
      {runsDevTools ? (
        <iframe
          src={devtools.url}
          title={`DevTools for ${page} in ${name}'s browser`}
          className="min-h-0 w-full flex-1 border-0 bg-white"
          referrerPolicy="no-referrer"
          allow="clipboard-read; clipboard-write"
        />
      ) : (
        <ElsewhereNotice url={devtools.url} onPopOut={onPopOut} />
      )}
    </section>
  )
}

/** Where DevTools can't run: their address, for a Chromium browser. */
function ElsewhereNotice({
  url,
  onPopOut,
}: {
  url: string
  onPopOut: () => void
}) {
  const [copied, setCopied] = useState(false)
  return (
    <div className="grid content-start gap-2 overflow-auto p-3 text-xs">
      <p className="max-w-prose text-muted-foreground">
        Chrome's DevTools run in Chrome, Edge or another Chromium browser, not
        in this one. Open their address in one: it works until the browser goes
        to sleep.
      </p>
      <div className="flex min-w-0 items-center gap-1.5">
        <code className="min-w-0 flex-1 truncate rounded bg-muted px-2 py-1 font-mono">
          {url}
        </code>
        <Button
          size="xs"
          variant="outline"
          onClick={() => {
            void navigator.clipboard
              .writeText(url)
              .then(() => setCopied(true))
              .catch(() => setCopied(false))
          }}
        >
          {copied ? <CheckIcon /> : <CopyIcon />}
          {copied ? "Copied" : "Copy"}
        </Button>
        <Button size="xs" variant="outline" onClick={onPopOut}>
          <ArrowSquareOutIcon />
          Open
        </Button>
      </div>
    </div>
  )
}
