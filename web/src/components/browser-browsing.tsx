import { BrowserIcon } from "@phosphor-icons/react"
import {
  BrowserSteps,
  lastShot,
  useBrowserRuns,
} from "@/components/browser-rail"
import { screenshotURL } from "@/components/browser-window"
import { Button } from "@/components/ui/button"
import { $api, type Approval } from "@/lib/api/client"
import { tileClass } from "@/lib/accents"
import { hostOf } from "@/lib/browser-sites"
import { cn } from "@/lib/utils"

/** A session that went quiet this long ago no longer shows as browsing now. */
const recent = 15 * 60 * 1000

/** Whether an approval is a question the teammate's browser asks on its page. */
export function isBrowserAsk(a: Approval, teammate: string) {
  return (
    (a.kind === "browser.action" || a.kind === "browser.credential") &&
    (a.browserAction?.teammateId ?? a.browserCredential?.teammateId) ===
      teammate
  )
}

/** What the teammate's browser does now: its latest session while the browser runs. */
export function useBrowsing(env: string, teammate: string) {
  const status = $api.useQuery(
    "get",
    "/api/environments/{env}/teammates/{teammate}/browser",
    { params: { path: { env, teammate } } },
    { refetchInterval: 10000 }
  )
  const { runs } = useBrowserRuns(env, teammate)
  const run = runs.at(0)
  const last = run?.steps.at(-1)
  const running = status.data?.vm === "running"
  const now =
    running && !!last && Date.now() - new Date(last.at).getTime() < recent
  return { run, last, running, now, url: status.data?.url }
}

/**
 * Browsing in the thread: a picture of the page, the last steps, and Watch, which opens the
 * window beside the conversation. Questions on the page wait with the other approvals.
 */
export function BrowsingCard({
  env,
  teammate,
  name,
  waiting,
  watching,
  onWatch,
}: {
  env: string
  teammate: string
  name: string
  waiting: boolean
  watching: boolean
  onWatch: () => void
}) {
  const b = useBrowsing(env, teammate)
  if (!b.now || !b.run) return null
  const steps = b.run.steps
  const shot = lastShot(steps)
  const host = hostOf(b.last?.url ?? b.url)
  return (
    <div
      role="group"
      aria-label={`${name}'s browser`}
      className="ml-8 grid gap-3 rounded-lg border p-3 text-xs"
    >
      <div className="flex items-center gap-2">
        <span
          className={cn(
            "inline-flex size-5 items-center justify-center rounded",
            tileClass("browser")
          )}
        >
          <BrowserIcon className="size-3" weight="bold" />
        </span>
        <span className="min-w-0 flex-1 truncate">
          Browsing <span className="font-mono">{host}</span>
          <span className="text-muted-foreground">
            {" "}
            · {steps.length} {steps.length === 1 ? "step" : "steps"}
            {waiting && " · waiting for you below"}
          </span>
        </span>
        {!watching && (
          <Button size="xs" variant="outline" onClick={onWatch}>
            Watch
          </Button>
        )}
      </div>
      <div className="flex gap-3 max-sm:flex-col">
        {shot && (
          <button
            type="button"
            onClick={onWatch}
            aria-label="Watch the browser"
            className="relative shrink-0 self-start"
          >
            <img
              alt=""
              src={screenshotURL(env, teammate, shot)}
              className="w-48 rounded border"
            />
            <LiveTag waiting={waiting} />
          </button>
        )}
        <div className="min-w-0 flex-1">
          <BrowserSteps
            env={env}
            teammate={teammate}
            name={name}
            steps={steps.slice(-3)}
            replay={null}
            onReplay={onWatch}
          />
        </div>
      </div>
    </div>
  )
}

/** The rail's line for the browser, while it is in use. */
export function BrowsingNow({
  env,
  teammate,
  waiting,
  onWatch,
}: {
  env: string
  teammate: string
  waiting: boolean
  onWatch: () => void
}) {
  const b = useBrowsing(env, teammate)
  if (!b.running) return null
  const shot = b.run && lastShot(b.run.steps)
  return (
    <button
      type="button"
      onClick={onWatch}
      className="grid w-full gap-1.5 text-left"
    >
      {shot && b.now && (
        <span className="relative">
          <img
            alt=""
            src={screenshotURL(env, teammate, shot)}
            className="w-full rounded border"
          />
          <LiveTag waiting={waiting} />
        </span>
      )}
      <span className="flex items-center gap-1.5">
        <BrowserIcon className="size-3.5 shrink-0 text-(--acc-browser)" />
        <span className="truncate">
          {b.now ? "Browsing " : "Browser open on "}
          <span className="font-mono">
            {hostOf(b.last?.url ?? b.url) || "a blank page"}
          </span>
        </span>
        <span className="ml-auto text-muted-foreground">Watch</span>
      </span>
    </button>
  )
}

function LiveTag({ waiting }: { waiting: boolean }) {
  return (
    <span className="absolute bottom-1.5 left-1.5 inline-flex items-center gap-1 rounded bg-black/60 px-1 py-px font-mono text-[0.5625rem] text-white">
      <span
        className={cn(
          "size-1.5 rounded-full",
          waiting ? "bg-(--st-waiting)" : "bg-(--st-running)"
        )}
      />
      {waiting ? "WAITING" : "LIVE"}
    </span>
  )
}
