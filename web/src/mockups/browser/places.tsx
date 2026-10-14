import { useState, type Dispatch, type ReactNode } from "react"
import {
  ArrowsOutSimpleIcon,
  BrowserIcon,
  DotsThreeIcon,
  FolderSimpleIcon,
  GlobeIcon,
  PaperPlaneRightIcon,
  XIcon,
} from "@phosphor-icons/react"
import { StatusDot } from "@/components/status-dot"
import { PresenceDot } from "@/components/teammate-avatar"
import { Button } from "@/components/ui/button"
import { presenceLabels, type Presence } from "@/hooks/use-teammate-presence"
import { tileClass } from "@/lib/accents"
import { cn } from "@/lib/utils"
import { script, task, teammate, terminalLines } from "./data"
import { BrowserRail, Steps, Thumb, type RailTab } from "./rail"
import type { Sim, SimAction } from "./sim"
import { BrowserWindow, hostOf } from "./window"

export const places = ["teammate", "workspace", "conversation"] as const
export type Place = (typeof places)[number]

export const placeNames: Record<Place, string> = {
  teammate: "Teammate › Browser",
  workspace: "Sandbox workspace",
  conversation: "Conversation",
}

type PlaceProps = {
  sim: Sim
  dispatch: Dispatch<SimAction>
  go: (place: Place) => void
}

export function presenceOf(sim: Sim): Presence {
  if (sim.pending) return "waiting"
  if (sim.working && sim.vm === "running") return "working"
  return "idle"
}

/** The teammate page's Browser tab: the window, big, with its activity and sites beside it. */
export function TeammatePlace({ sim, dispatch }: PlaceProps) {
  const [rail, setRail] = useState<RailTab>("activity")
  return (
    <>
      <TeammateHeader sim={sim} tab="Browser" />
      <div className="flex min-h-0 flex-1">
        <div className="flex min-w-0 flex-1 flex-col p-3">
          <BrowserWindow
            sim={sim}
            dispatch={dispatch}
            size="full"
            onSites={() => setRail("sites")}
          />
        </div>
        <BrowserRail sim={sim} dispatch={dispatch} tab={rail} onTab={setRail} />
      </div>
    </>
  )
}

/** A sandbox's workspace: the terminal, with the same window as a side panel. */
export function WorkspacePlace({ sim, dispatch, go }: PlaceProps) {
  const [open, setOpen] = useState(true)
  return (
    <>
      <header className="flex h-12 shrink-0 items-center gap-2 border-b px-4 text-sm">
        <StatusDot status="running" compact />
        <h1 className="font-mono font-medium">{task.sandbox}</h1>
        <span className="text-xs text-muted-foreground">
          {teammate.avatar} Finn · node@3
        </span>
        <div className="ml-auto flex items-center gap-1">
          <PanelToggle
            icon={<BrowserIcon />}
            label="Browser"
            active={open}
            badge={sim.pending}
            onClick={() => setOpen(!open)}
          />
          <PanelToggle icon={<GlobeIcon />} label="Connections" />
          <PanelToggle icon={<FolderSimpleIcon />} label="Files" />
          <Button size="icon-sm" variant="ghost" aria-label="Sandbox options">
            <DotsThreeIcon weight="bold" />
          </Button>
        </div>
      </header>
      <div className="flex min-h-0 flex-1">
        <Terminal sim={sim} />
        {open && (
          <BrowserPanel
            sim={sim}
            dispatch={dispatch}
            onClose={() => setOpen(false)}
            onExpand={() => go("teammate")}
          />
        )}
      </div>
    </>
  )
}

/** The teammate's conversation, where browsing shows up as a live card in the thread. */
export function ConversationPlace({ sim, dispatch, go }: PlaceProps) {
  const [watching, setWatching] = useState(false)
  return (
    <>
      <TeammateHeader sim={sim} tab="Conversation" />
      <div className="flex min-h-0 flex-1">
        <div className="flex min-w-0 flex-1 flex-col">
          <div className="min-h-0 flex-1 overflow-y-auto">
            <div className="mx-auto grid max-w-2xl gap-4 p-6 text-sm">
              <div className="flex items-center gap-3 text-[0.6875rem] text-muted-foreground">
                <span className="h-px flex-1 bg-border" />
                Today
                <span className="h-px flex-1 bg-border" />
              </div>
              <div className="ml-auto max-w-[80%] rounded-2xl rounded-br-sm bg-primary px-3.5 py-2 text-primary-foreground">
                The staging webhook test gets a ping instead of a payment event.
                Can you send a real test event from the Pebble console and grab
                the event log?
              </div>
              <div className="flex gap-2.5">
                <span className="text-lg leading-none">{teammate.avatar}</span>
                <p className="max-w-[85%]">
                  On it. I'll sign in to the Pebble console with the team login,
                  send one to staging and export the log.
                </p>
              </div>
              <BrowsingCard
                sim={sim}
                dispatch={dispatch}
                watching={watching}
                onWatch={() => setWatching(true)}
              />
              {!sim.working && sim.log.length > 0 && sim.vm === "running" && (
                <div className="flex gap-2.5">
                  <span className="text-lg leading-none">
                    {teammate.avatar}
                  </span>
                  <p className="max-w-[85%]">
                    Done. The console now sends <code>payment.succeeded</code>;
                    I put the log in <code>api-refactor</code> and I'll fix the
                    fixture next.
                  </p>
                </div>
              )}
            </div>
          </div>
          <div className="mx-auto w-full max-w-2xl p-4 pt-0">
            <div className="flex items-center gap-2 rounded-xl border px-3 py-2 text-sm text-muted-foreground">
              Message Finn…
              <PaperPlaneRightIcon className="ml-auto size-4" />
            </div>
          </div>
        </div>
        {watching ? (
          <BrowserPanel
            sim={sim}
            dispatch={dispatch}
            onClose={() => setWatching(false)}
            onExpand={() => go("teammate")}
          />
        ) : (
          <aside className="w-72 shrink-0 border-l p-4 text-xs max-lg:hidden">
            <h2 className="font-mono text-[0.625rem] tracking-wider text-muted-foreground uppercase">
              Now
            </h2>
            {sim.vm === "running" ? (
              <button
                type="button"
                onClick={() => setWatching(true)}
                className="mt-2 grid w-full gap-1.5 text-left"
              >
                <span className="relative">
                  <Thumb view={sim.view} scale={0.234} />
                  <LiveTag waiting={sim.pending} />
                </span>
                <span className="flex items-center gap-1.5">
                  <BrowserIcon className="size-3.5 text-(--acc-browser)" />
                  <span className="truncate">
                    Browsing{" "}
                    <span className="font-mono">{hostOf(sim.view.url)}</span>
                  </span>
                  <span className="ml-auto text-muted-foreground">Watch</span>
                </span>
              </button>
            ) : (
              <p className="mt-2 text-muted-foreground">
                Nothing on its plate.
              </p>
            )}
          </aside>
        )}
      </div>
    </>
  )
}

/**
 * Browsing in the thread: a live picture of the page, the last steps, and Finn's question
 * with its answer buttons when it waits. Watch opens the window beside the conversation.
 */
function BrowsingCard({
  sim,
  dispatch,
  watching,
  onWatch,
}: {
  sim: Sim
  dispatch: Dispatch<SimAction>
  watching: boolean
  onWatch: () => void
}) {
  if (sim.log.length === 0 && sim.vm !== "running") return null
  const ask = sim.pending ? script[sim.next]?.ask : undefined
  return (
    <div className="ml-8 grid gap-3 rounded-lg border p-3 text-xs">
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
          {sim.working ? "Browsing " : "Browsed "}
          <span className="font-mono">{hostOf(sim.view.url)}</span>
          <span className="text-muted-foreground">
            {" "}
            · {sim.log.length} steps
          </span>
        </span>
        {!watching && (
          <Button size="xs" variant="outline" onClick={onWatch}>
            Watch
          </Button>
        )}
      </div>
      <div className="flex gap-3 max-sm:flex-col">
        <button
          type="button"
          onClick={onWatch}
          aria-label="Watch the browser"
          className="relative shrink-0 self-start"
        >
          <Thumb view={sim.view} scale={0.25} />
          {sim.working && <LiveTag waiting={sim.pending} />}
        </button>
        <div className="min-w-0 flex-1">
          <Steps
            sim={sim}
            dispatch={(a) => {
              dispatch(a)
              if (a.type === "replay") onWatch()
            }}
            entries={sim.log.slice(-3)}
          />
        </div>
      </div>
      {ask && (
        <div className="flex flex-wrap items-center gap-2 rounded-md bg-(--st-waiting)/10 p-2">
          <span className="min-w-0 flex-1">
            {ask.kind === "credential" ? (
              <>
                Fill <b>{ask.field}</b> with{" "}
                <span className="font-mono">{ask.secret}</span>?
              </>
            ) : ask.kind === "download" ? (
              <>
                Download <span className="font-mono">{ask.file}</span>?
              </>
            ) : (
              <>
                Click <b>“{ask.name}”</b> on console.pebble.dev?
              </>
            )}
          </span>
          <Button
            size="xs"
            variant="ghost"
            onClick={() => dispatch({ type: "decide", how: "deny" })}
          >
            Deny
          </Button>
          <Button
            size="xs"
            onClick={() => dispatch({ type: "decide", how: "allow" })}
          >
            {ask.kind === "credential"
              ? "Fill"
              : ask.kind === "download"
                ? "Download"
                : "Allow once"}
          </Button>
        </div>
      )}
    </div>
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

/** The window as a side panel, with the last steps folded under it. */
function BrowserPanel({
  sim,
  dispatch,
  onClose,
  onExpand,
}: {
  sim: Sim
  dispatch: Dispatch<SimAction>
  onClose: () => void
  onExpand: () => void
}) {
  const [steps, setSteps] = useState(false)
  return (
    <aside className="flex w-[30rem] shrink-0 flex-col border-l max-md:fixed max-md:inset-0 max-md:z-30 max-md:w-auto max-md:bg-background">
      <div className="flex h-10 shrink-0 items-center gap-2 border-b px-3 text-xs">
        <span
          className={cn(
            "inline-flex size-5 items-center justify-center rounded",
            tileClass("browser")
          )}
        >
          <BrowserIcon className="size-3" weight="bold" />
        </span>
        <span className="font-medium">Finn's browser</span>
        <span className="ml-auto flex items-center gap-0.5">
          <Button
            size="icon-sm"
            variant="ghost"
            aria-label="Open on Finn's page"
            title="Open on Finn's page"
            onClick={onExpand}
          >
            <ArrowsOutSimpleIcon />
          </Button>
          <Button
            size="icon-sm"
            variant="ghost"
            aria-label="Close the panel"
            onClick={onClose}
          >
            <XIcon />
          </Button>
        </span>
      </div>
      <BrowserWindow
        sim={sim}
        dispatch={dispatch}
        size="panel"
        onExpand={onExpand}
      />
      {sim.log.length > 0 && (
        <div className="shrink-0 border-t text-xs">
          <button
            type="button"
            aria-expanded={steps}
            onClick={() => setSteps(!steps)}
            className="flex h-9 w-full items-center gap-2 px-3 text-left hover:bg-muted/60"
          >
            <span className="font-medium">Steps</span>
            <span className="min-w-0 flex-1 truncate text-muted-foreground">
              {sim.log.at(-1)?.text}
            </span>
            <span className="font-mono text-muted-foreground">
              {sim.log.length}
            </span>
          </button>
          {steps && (
            <div className="max-h-56 overflow-y-auto px-2 pb-2">
              <Steps sim={sim} dispatch={dispatch} entries={sim.log} />
            </div>
          )}
        </div>
      )}
    </aside>
  )
}

function PanelToggle({
  icon,
  label,
  active = false,
  badge = false,
  onClick,
}: {
  icon: ReactNode
  label: string
  active?: boolean
  badge?: boolean
  onClick?: () => void
}) {
  return (
    <Button
      size="sm"
      variant={active ? "secondary" : "ghost"}
      aria-pressed={active}
      onClick={onClick}
      className={cn(
        "relative",
        label === "Browser" && "[&_svg]:text-(--acc-browser)",
        label === "Connections" && "[&_svg]:text-(--acc-network)",
        label === "Files" && "[&_svg]:text-(--acc-git)"
      )}
    >
      {icon}
      <span className="max-md:hidden">{label}</span>
      {badge && (
        <span className="absolute top-1 right-1 size-1.5 rounded-full bg-(--st-waiting)" />
      )}
    </Button>
  )
}

function Terminal({ sim }: { sim: Sim }) {
  return (
    <div className="min-w-0 flex-1 overflow-auto bg-neutral-950 p-4 font-mono text-xs/relaxed text-neutral-200 max-md:hidden">
      {terminalLines.map((l, i) => (
        <div
          key={i}
          className={cn(
            "whitespace-pre",
            l.includes("✘") && "text-red-400",
            l.includes("✓") && "text-emerald-400",
            l.startsWith("finn") || l.startsWith(" ") ? "text-sky-300" : ""
          )}
        >
          {l || " "}
        </div>
      ))}
      <div className="whitespace-pre text-neutral-400">
        {sim.pending
          ? "  … waiting for an approval in Studio"
          : sim.working
            ? `  → ${sim.log.at(-1)?.text ?? "starting the browser"}`
            : "  ✓ done"}
      </div>
    </div>
  )
}

function TeammateHeader({ sim, tab }: { sim: Sim; tab: string }) {
  const presence = presenceOf(sim)
  return (
    <>
      <header className="flex h-12 shrink-0 items-center gap-2 border-b px-4">
        <span className="text-base leading-none">{teammate.avatar}</span>
        <h1 className="text-sm font-medium">Finn</h1>
        <span className="flex items-center gap-1.5 text-xs text-muted-foreground">
          <PresenceDot presence={presence} />
          {presenceLabels[presence]}
        </span>
      </header>
      <div className="flex shrink-0 gap-1 overflow-x-auto border-b px-3 py-2 text-xs">
        {[
          "Conversation",
          "Tasks",
          "Chats",
          "Memory",
          "Browser",
          "Settings",
        ].map((t) => (
          <span
            key={t}
            className={cn(
              "flex h-7 items-center gap-1.5 rounded-md px-2.5",
              t === tab ? "bg-muted font-medium" : "text-muted-foreground"
            )}
          >
            {t}
            {t === "Browser" && sim.working && sim.vm === "running" && (
              <span
                className={cn(
                  "size-1.5 rounded-full",
                  sim.pending ? "bg-(--st-waiting)" : "bg-(--st-running)"
                )}
              />
            )}
          </span>
        ))}
      </div>
    </>
  )
}
