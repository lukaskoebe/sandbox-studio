import { useState, type Dispatch } from "react"
import {
  CaretDownIcon,
  CaretRightIcon,
  ChatCircleIcon,
  EyeSlashIcon,
  HandIcon,
  KeyIcon,
} from "@phosphor-icons/react"
import { StatusDot } from "@/components/status-dot"
import { cn } from "@/lib/utils"
import { earlier, sites, task, teammate } from "./data"
import { FakeSite, pageHeight, pageWidth } from "./site"
import { PolicyPicker } from "./window"
import type { LogEntry, Sim, SimAction } from "./sim"

export type RailTab = "activity" | "sites"

/** Beside the full browser: what happened, step by step, and what Finn may do where. */
export function BrowserRail({
  sim,
  dispatch,
  tab,
  onTab,
}: {
  sim: Sim
  dispatch: Dispatch<SimAction>
  tab: RailTab
  onTab: (t: RailTab) => void
}) {
  return (
    <aside className="flex w-80 shrink-0 flex-col border-l max-xl:hidden">
      <div role="tablist" className="flex shrink-0 gap-4 border-b px-4 text-xs">
        {(
          [
            ["activity", "Activity"],
            ["sites", "Sites"],
          ] as const
        ).map(([id, label]) => (
          <button
            key={id}
            type="button"
            role="tab"
            aria-selected={tab === id}
            onClick={() => onTab(id)}
            className={cn(
              "-mb-px h-10 border-b-2",
              tab === id
                ? "border-foreground font-medium"
                : "border-transparent text-muted-foreground hover:text-foreground"
            )}
          >
            {label}
          </button>
        ))}
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto">
        {tab === "activity" ? (
          <Activity sim={sim} dispatch={dispatch} />
        ) : (
          <Sites sim={sim} dispatch={dispatch} />
        )}
      </div>
    </aside>
  )
}

function Activity({
  sim,
  dispatch,
}: {
  sim: Sim
  dispatch: Dispatch<SimAction>
}) {
  const [showEarlier, setShowEarlier] = useState(false)
  return (
    <div className="grid gap-4 p-4 text-xs">
      {sim.log.length > 0 && (
        <section className="grid gap-2">
          <div className="grid gap-0.5">
            <span className="font-mono text-[0.625rem] tracking-wider text-muted-foreground uppercase">
              {sim.working ? "Now" : "Today"}
            </span>
            <span className="font-medium">{task.title}</span>
            <span className="flex items-center gap-1 text-muted-foreground">
              <ChatCircleIcon className="size-3" />
              Chat in <span className="font-mono">{task.sandbox}</span>
            </span>
          </div>
          <Steps sim={sim} dispatch={dispatch} entries={sim.log} thumbs />
        </section>
      )}
      <section className="grid gap-2">
        <button
          type="button"
          aria-expanded={showEarlier}
          onClick={() => setShowEarlier(!showEarlier)}
          className="flex items-start gap-1.5 text-left"
        >
          {showEarlier ? (
            <CaretDownIcon className="mt-0.5 size-3 shrink-0" />
          ) : (
            <CaretRightIcon className="mt-0.5 size-3 shrink-0" />
          )}
          <span className="grid gap-0.5">
            <span className="font-mono text-[0.625rem] tracking-wider text-muted-foreground uppercase">
              {earlier.when}
            </span>
            <span>{earlier.title}</span>
            <span className="text-muted-foreground">
              {earlier.steps} steps · from{" "}
              <span className="font-mono">{earlier.sandbox}</span>
            </span>
          </span>
        </button>
        {showEarlier && (
          <p className="pl-4.5 text-muted-foreground">
            Older sessions replay the same way, from their screenshots.
          </p>
        )}
      </section>
    </div>
  )
}

/**
 * The log as a list of steps. Clicking one replays the page as it was after it; a step that
 * changed the page shows a small picture of it.
 */
export function Steps({
  sim,
  dispatch,
  entries,
  thumbs = false,
}: {
  sim: Sim
  dispatch: Dispatch<SimAction>
  entries: LogEntry[]
  thumbs?: boolean
}) {
  return (
    <ol className="grid">
      {entries.map((e, i) => {
        const prev = entries[i - 1]
        const changed = i === 0 || prev.view.url !== e.view.url
        const active = sim.replay === e.id
        return (
          <li key={e.id} className="relative pl-5">
            <span
              aria-hidden
              className={cn(
                "absolute top-2.5 left-[5px] size-2 rounded-full",
                e.outcome === "waiting"
                  ? "bg-(--st-waiting)"
                  : e.outcome === "denied"
                    ? "bg-(--st-failed)"
                    : e.actor === "you"
                      ? "bg-primary"
                      : "bg-muted-foreground/40"
              )}
            />
            {i < entries.length - 1 && (
              <span
                aria-hidden
                className="absolute top-5 bottom-0 left-[8px] w-px bg-border"
              />
            )}
            <button
              type="button"
              onClick={() =>
                dispatch({
                  type: "replay",
                  entry: e.outcome === "waiting" || active ? null : e.id,
                })
              }
              className={cn(
                "grid w-full gap-1 rounded-md px-2 py-1.5 text-left hover:bg-muted",
                active && "bg-muted ring-1 ring-border"
              )}
            >
              <span className="flex items-baseline gap-1.5">
                <span className="shrink-0 font-mono text-[0.625rem] text-muted-foreground">
                  {e.time}
                </span>
                <span className="min-w-0 flex-1">
                  {e.actor === "you" ? (
                    <HandIcon className="mr-1 inline size-3 text-primary" />
                  ) : (
                    <span className="mr-1">{teammate.avatar}</span>
                  )}
                  {e.text}
                </span>
              </span>
              {e.outcome === "waiting" ? (
                <StatusDot
                  status="waiting"
                  label="Waiting for you, on the page"
                  className="text-[0.6875rem]"
                />
              ) : e.outcome === "denied" ? (
                <span className="text-[0.6875rem] text-muted-foreground">
                  Denied
                </span>
              ) : e.outcome === "allowed" ? (
                <span className="flex items-center gap-1 text-[0.6875rem] text-muted-foreground">
                  {e.ask?.kind === "credential" && (
                    <KeyIcon className="size-3" />
                  )}
                  Allowed by you
                </span>
              ) : null}
              {thumbs && changed && e.view.page !== "held" && (
                <Thumb view={e.view} scale={0.17} />
              )}
            </button>
          </li>
        )
      })}
    </ol>
  )
}

/** A small, static picture of a page. */
export function Thumb({
  view,
  scale,
  className,
}: {
  view: LogEntry["view"]
  scale: number
  className?: string
}) {
  return (
    <span
      aria-hidden
      className={cn(
        "pointer-events-none block overflow-hidden rounded border bg-white",
        className
      )}
      style={{ width: pageWidth * scale, height: pageHeight * scale }}
    >
      <span
        className="block origin-top-left"
        style={{ transform: `scale(${scale})` }}
      >
        <FakeSite view={view} />
      </span>
    </span>
  )
}

/** Every site Finn's profile knows, with what Finn may do there, in plain words. */
function Sites({ sim, dispatch }: { sim: Sim; dispatch: Dispatch<SimAction> }) {
  return (
    <div className="grid gap-3 p-4 text-xs">
      <p className="text-muted-foreground">
        Reading pages is always allowed; scripts, cookies and storage never.
        Uploads and downloads always ask.
      </p>
      {sites.map((s) => (
        <section key={s.host} className="grid gap-2 rounded-lg border p-3">
          <div className="flex items-center gap-2">
            <span className="min-w-0 flex-1 truncate font-mono font-medium">
              {s.host}
            </span>
            <span className="text-muted-foreground">{s.lastUsed}</span>
          </div>
          <PolicyPicker
            value={sim.policies[s.host] ?? s.actions}
            onChange={(p) =>
              dispatch({ type: "policy", host: s.host, policy: p })
            }
          />
          <div className="flex flex-wrap gap-1">
            {s.signedIn && (
              <span className="rounded bg-muted px-1.5 py-0.5">Signed in</span>
            )}
            {s.login && (
              <span className="inline-flex items-center gap-1 rounded bg-muted px-1.5 py-0.5">
                <KeyIcon className="size-3" />
                <span className="font-mono">{s.login}</span>
              </span>
            )}
            {s.hidden.map((f) => (
              <span
                key={f}
                className="inline-flex items-center gap-1 rounded bg-muted px-1.5 py-0.5"
              >
                <EyeSlashIcon className="size-3" />
                {f}
              </span>
            ))}
          </div>
        </section>
      ))}
      <button
        type="button"
        className="justify-self-start text-muted-foreground underline-offset-4 hover:text-foreground hover:underline"
        onClick={() =>
          dispatch({
            type: "note",
            text: "Pick a field on the page to hide it from Finn",
          })
        }
      >
        Hide a field from Finn…
      </button>
    </div>
  )
}
