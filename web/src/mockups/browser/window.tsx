import {
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type CSSProperties,
  type Dispatch,
  type FormEvent,
  type ReactNode,
} from "react"
import {
  ArrowClockwiseIcon,
  ArrowLeftIcon,
  ArrowRightIcon,
  ArrowsOutSimpleIcon,
  CaretLeftIcon,
  CaretRightIcon,
  CheckIcon,
  ClockCounterClockwiseIcon,
  CursorClickIcon,
  DetectiveIcon,
  DotsThreeIcon,
  DownloadSimpleIcon,
  EyeSlashIcon,
  HandIcon,
  KeyIcon,
  MoonIcon,
  NavigationArrowIcon,
  PowerIcon,
  ShieldCheckIcon,
  ShieldWarningIcon,
  SignOutIcon,
  XIcon,
} from "@phosphor-icons/react"
import { KindChip } from "@/components/kind-chip"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Spinner } from "@/components/ui/spinner"
import { ruleClass, tileClass } from "@/lib/accents"
import { cn } from "@/lib/utils"
import {
  sandboxes,
  script,
  sites,
  task,
  teammate,
  throwaway,
  type Ask,
  type View,
} from "./data"
import { FakeSite, pageHeight, pageWidth } from "./site"
import { agentTarget, type Sim, type SimAction } from "./sim"

type Props = {
  sim: Sim
  dispatch: Dispatch<SimAction>
  size: "full" | "panel"
  /** Opens the browser on its own page, from the panel. */
  onExpand?: () => void
  /** Shows the Sites list, from the site info's "All sites". */
  onSites?: () => void
}

export function hostOf(url: string) {
  try {
    return new URL(url).host
  } catch {
    return url
  }
}

function titleOf(view: View) {
  switch (view.page) {
    case "login":
      return "Sign in · Pebble"
    case "overview":
      return "Overview · Pebble"
    case "webhooks":
      return "Webhooks · Pebble"
    case "changelog":
      return "Changelog · Pebble"
    case "held":
      return `Held · ${hostOf(view.url)}`
    case "other":
      return hostOf(view.url)
  }
}

/** The view the window shows: a replayed step, the throwaway session, or the live page. */
export function shownView(sim: Sim): View {
  if (sim.replay !== null) return sim.log[sim.replay]?.view ?? sim.view
  if (sim.tab === "throwaway") return throwaway.view
  return sim.view
}

/**
 * Finn's browser as a window: tabs for the profile and throwaway sessions, a toolbar like any
 * browser's, a bar that says who drives, and the page. The same window sits on the teammate's
 * Browser tab, beside a sandbox's terminal and beside the conversation.
 */
export function BrowserWindow({
  sim,
  dispatch,
  size,
  onExpand,
  onSites,
}: Props) {
  const [resetting, setResetting] = useState(false)
  return (
    <div
      className={cn(
        "relative flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden bg-background",
        size === "full" && "rounded-lg border shadow-xs"
      )}
    >
      <TabStrip sim={sim} dispatch={dispatch} size={size} />
      <Toolbar
        sim={sim}
        dispatch={dispatch}
        size={size}
        onExpand={onExpand}
        onSites={onSites}
        onReset={() => setResetting(true)}
      />
      <DriverBar sim={sim} dispatch={dispatch} size={size} />
      <Viewport sim={sim} dispatch={dispatch} size={size} />
      {sim.note && (
        <div
          role="status"
          className="pointer-events-none absolute bottom-3 left-1/2 z-20 -translate-x-1/2 animate-[tm-in_.2s_ease-out] rounded-md bg-foreground px-3 py-1.5 text-xs whitespace-nowrap text-background shadow-lg"
        >
          {sim.note}
        </div>
      )}
      <AlertDialog open={resetting} onOpenChange={setResetting}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Reset Finn's browser profile?</AlertDialogTitle>
            <AlertDialogDescription>
              Finn is signed out of every site:{" "}
              {sites.filter((s) => s.signedIn).length} today. Cookies and site
              data go; the history, downloads and site rules stay.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              onClick={() => {
                dispatch({ type: "reset" })
                setResetting(false)
              }}
            >
              Reset profile
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}

/**
 * The profile and the throwaway sessions agents opened, as tabs: the page's title in each, the
 * profile's face or a throwaway's mask in front of it, like any browser's tabs.
 */
function TabStrip({ sim, dispatch, size }: Omit<Props, "onExpand">) {
  const tab = (
    id: Sim["tab"],
    icon: ReactNode,
    label: string,
    title: string,
    extra?: ReactNode,
    close?: () => void
  ) => {
    const active = sim.tab === id
    return (
      <div
        className={cn(
          "group relative flex h-8 min-w-0 items-center gap-1.5 rounded-t-lg border border-transparent px-2.5 text-xs",
          size === "full" ? "w-56" : "max-w-44 flex-1",
          active
            ? "-mb-px border-border border-b-background bg-background text-foreground"
            : "text-muted-foreground hover:bg-background/50 hover:text-foreground"
        )}
      >
        <button
          type="button"
          title={title}
          onClick={() => dispatch({ type: "tab", tab: id })}
          className="flex min-w-0 flex-1 items-center gap-2 text-left outline-none after:absolute after:inset-0 focus-visible:underline"
        >
          <span className="flex size-4 shrink-0 items-center justify-center">
            {icon}
          </span>
          <span className="truncate">{label}</span>
          {extra}
        </button>
        {close && (
          <button
            type="button"
            aria-label="End the throwaway session"
            title="End the session: its cookies and storage are gone"
            onClick={close}
            className="relative z-10 -mr-1 inline-flex size-5 shrink-0 items-center justify-center rounded-full hover:bg-muted"
          >
            <XIcon className="size-3" />
          </button>
        )}
      </div>
    )
  }
  if (sim.vm !== "running") return null
  return (
    <div className="flex h-10 shrink-0 items-end gap-1 border-b bg-muted/50 px-2">
      {tab(
        "profile",
        <span className="text-[0.8125rem] leading-none">
          {teammate.avatar}
        </span>,
        titleOf(sim.view),
        "Finn's own profile: logins and cookies stay between sessions"
      )}
      {sim.throwawayOpen &&
        tab(
          "throwaway",
          <DetectiveIcon className="size-3.5" />,
          titleOf(throwaway.view),
          `A throwaway session an agent in ${throwaway.sandbox} opened: a temporary profile that is gone when it closes`,
          size === "full" && (
            <span className="ml-auto shrink-0 font-mono text-[0.625rem] text-muted-foreground">
              {throwaway.sandbox}
            </span>
          ),
          () => dispatch({ type: "closeThrowaway" })
        )}
      <span className="mb-3 ml-auto inline-flex shrink-0 items-center gap-1 pl-2 font-mono text-[0.625rem] text-muted-foreground">
        {sim.replay !== null ? (
          <>
            <ClockCounterClockwiseIcon className="size-3" />
            REPLAY
          </>
        ) : (
          <>
            <span className="size-1.5 animate-pulse rounded-full bg-(--st-running)" />
            LIVE
          </>
        )}
      </span>
    </div>
  )
}

function Toolbar({
  sim,
  dispatch,
  size,
  onExpand,
  onSites,
  onReset,
}: Props & { onReset: () => void }) {
  const view = shownView(sim)
  const live = sim.replay === null && sim.tab === "profile"
  const running = sim.vm === "running"
  return (
    <div className="flex shrink-0 items-center gap-1 border-b px-2 py-1.5">
      {running && (
        <>
          <Button
            size="icon-sm"
            variant="ghost"
            aria-label="Back"
            disabled={!live || sim.back.length === 0 || sim.driver !== "you"}
            onClick={() => dispatch({ type: "history", dir: "back" })}
          >
            <ArrowLeftIcon />
          </Button>
          {size === "full" && (
            <Button
              size="icon-sm"
              variant="ghost"
              aria-label="Forward"
              disabled={
                !live || sim.forward.length === 0 || sim.driver !== "you"
              }
              onClick={() => dispatch({ type: "history", dir: "forward" })}
            >
              <ArrowRightIcon />
            </Button>
          )}
          <Button
            size="icon-sm"
            variant="ghost"
            aria-label="Reload"
            disabled={!live}
            onClick={() => dispatch({ type: "reload" })}
          >
            <ArrowClockwiseIcon />
          </Button>
          <Address
            key={`${view.url}-${sim.replay}-${sim.tab}`}
            sim={sim}
            dispatch={dispatch}
            view={view}
            editable={live}
            onSites={onSites}
          />
          <Downloads sim={sim} dispatch={dispatch} />
        </>
      )}
      {!running && (
        <span className="flex-1 truncate px-1 text-xs text-muted-foreground">
          {sim.vm === "waking" ? "Waking up…" : "Asleep"}
        </span>
      )}
      <DropdownMenu modal={false}>
        <DropdownMenuTrigger
          render={
            <Button
              variant="ghost"
              size="icon-sm"
              aria-label="Browser options"
              title="Browser options"
            />
          }
        >
          <DotsThreeIcon weight="bold" />
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="w-60">
          <DropdownMenuGroup>
            {onExpand && (
              <DropdownMenuItem onClick={onExpand}>
                <ArrowsOutSimpleIcon />
                Open on Finn's page
              </DropdownMenuItem>
            )}
            {running ? (
              <DropdownMenuItem onClick={() => dispatch({ type: "sleep" })}>
                <MoonIcon />
                Put to sleep
              </DropdownMenuItem>
            ) : (
              <DropdownMenuItem onClick={() => dispatch({ type: "wake" })}>
                <PowerIcon />
                Wake up
              </DropdownMenuItem>
            )}
            <DropdownMenuItem
              onClick={() =>
                dispatch({
                  type: "note",
                  text: "Opens Network › Rules, filtered to Finn's browser",
                })
              }
            >
              <ShieldCheckIcon />
              Network rules for this browser
            </DropdownMenuItem>
          </DropdownMenuGroup>
          <DropdownMenuSeparator />
          <DropdownMenuGroup>
            <DropdownMenuItem variant="destructive" onClick={onReset}>
              <SignOutIcon />
              Reset profile…
            </DropdownMenuItem>
          </DropdownMenuGroup>
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  )
}

/**
 * The address bar. Always editable: opening a page while Finn drives takes the wheel, and the
 * bar says so while you type. The shield in front opens what Finn may do on the site.
 */
function Address({
  sim,
  dispatch,
  view,
  editable,
  onSites,
}: {
  sim: Sim
  dispatch: Dispatch<SimAction>
  view: View
  editable: boolean
  onSites?: () => void
}) {
  const [draft, setDraft] = useState(view.url)
  const [focused, setFocused] = useState(false)
  const held = view.page === "held"
  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (!editable || !draft.trim()) return
    dispatch({ type: "navigate", url: draft.trim() })
    ;(document.activeElement as HTMLElement | null)?.blur()
  }
  const shown = focused ? draft : draft.replace(/^https:\/\//, "")
  return (
    <form
      onSubmit={submit}
      className={cn(
        "relative flex h-8 min-w-0 flex-1 items-center rounded-md border bg-muted/40 pr-1 focus-within:border-ring focus-within:bg-background focus-within:ring-2 focus-within:ring-ring/30",
        !editable && "opacity-80"
      )}
    >
      <SiteInfo
        host={hostOf(view.url)}
        held={held}
        sim={sim}
        dispatch={dispatch}
        onSites={onSites}
      />
      <input
        aria-label="Address"
        value={shown}
        readOnly={!editable}
        spellCheck={false}
        onFocus={(e) => {
          setFocused(true)
          requestAnimationFrame(() => e.target.select())
        }}
        onBlur={() => {
          setFocused(false)
          setDraft(view.url)
        }}
        onChange={(e) => setDraft(e.target.value)}
        className="h-full min-w-0 flex-1 bg-transparent font-mono text-xs outline-none"
      />
      {focused && editable && sim.driver === "agent" && (
        <span className="ml-1 shrink-0 rounded bg-muted px-1.5 py-0.5 text-[0.625rem] whitespace-nowrap text-muted-foreground max-sm:hidden">
          ↵ takes the wheel from Finn
        </span>
      )}
      {sim.replay !== null && (
        <span className="ml-1 shrink-0 rounded bg-muted px-1.5 py-0.5 text-[0.625rem] text-muted-foreground">
          Replay
        </span>
      )}
    </form>
  )
}

/** A small popover: a trigger and a panel that closes on a click outside or Escape. */
function Pop({
  trigger,
  label,
  align = "start",
  className,
  children,
}: {
  trigger: (open: boolean, toggle: () => void) => ReactNode
  label: string
  align?: "start" | "end"
  className?: string
  children: (close: () => void) => ReactNode
}) {
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!open) return
    const down = (e: PointerEvent) => {
      if (!ref.current?.contains(e.target as Node)) setOpen(false)
    }
    const key = (e: KeyboardEvent) => e.key === "Escape" && setOpen(false)
    document.addEventListener("pointerdown", down)
    document.addEventListener("keydown", key)
    return () => {
      document.removeEventListener("pointerdown", down)
      document.removeEventListener("keydown", key)
    }
  }, [open])
  return (
    <div ref={ref} className="relative shrink-0">
      {trigger(open, () => setOpen(!open))}
      {open && (
        <div
          role="dialog"
          aria-label={label}
          className={cn(
            "absolute top-full z-30 mt-2 w-80 rounded-lg border bg-popover p-3 text-xs text-popover-foreground shadow-lg",
            align === "end" ? "right-0" : "left-0",
            className
          )}
        >
          {children(() => setOpen(false))}
        </div>
      )}
    </div>
  )
}

const policyWords = {
  ask: "Ask first",
  allow: "Allowed",
  block: "Blocked",
} as const

/** What Finn may do on the site in the address bar, like a browser's site info. */
function SiteInfo({
  host,
  held,
  sim,
  dispatch,
  onSites,
}: {
  host: string
  held: boolean
  sim: Sim
  dispatch: Dispatch<SimAction>
  onSites?: () => void
}) {
  const site = sites.find((s) => s.host === host)
  const policy = sim.policies[host] ?? "ask"
  return (
    <Pop
      label={`What Finn may do on ${host}`}
      trigger={(open, toggle) => (
        <button
          type="button"
          aria-expanded={open}
          aria-label={`Site info for ${host}`}
          onClick={toggle}
          className={cn(
            "ml-0.5 inline-flex h-6 items-center gap-1 rounded px-1.5 hover:bg-muted",
            held ? "text-(--st-waiting)" : "text-muted-foreground"
          )}
        >
          {held ? (
            <ShieldWarningIcon className="size-3.5" weight="fill" />
          ) : (
            <ShieldCheckIcon className="size-3.5" />
          )}
        </button>
      )}
    >
      {(close) => (
        <div className="grid gap-3">
          <div>
            <div className="font-mono text-[0.8125rem] font-medium">{host}</div>
            <div className="text-muted-foreground">
              {held
                ? "Held: no rule covers this site yet."
                : sim.allowedHosts.includes(host)
                  ? "Reachable: a network rule allows it."
                  : "Reachable for this browser session."}
            </div>
          </div>
          {!held && (
            <>
              <PolicyPicker
                value={policy}
                onChange={(p) => dispatch({ type: "policy", host, policy: p })}
              />
              <dl className="grid grid-cols-[7.5rem_1fr] gap-y-1.5">
                <dt className="text-muted-foreground">Login</dt>
                <dd>
                  {site?.login ? (
                    <>
                      <span className="font-mono">{site.login}</span>, filled by
                      Studio when you allow it
                    </>
                  ) : (
                    "None"
                  )}
                </dd>
                <dt className="text-muted-foreground">Hidden from Finn</dt>
                <dd className="flex flex-wrap gap-1">
                  {["Passwords", ...(site?.hidden ?? [])].map((f) => (
                    <span
                      key={f}
                      className="inline-flex items-center gap-1 rounded bg-muted px-1.5 py-0.5"
                    >
                      <EyeSlashIcon className="size-3" />
                      {f}
                    </span>
                  ))}
                </dd>
                <dt className="text-muted-foreground">Signed in</dt>
                <dd>{site?.signedIn ? "Yes" : "No"}</dd>
              </dl>
            </>
          )}
          {onSites && (
            <button
              type="button"
              className="justify-self-start text-muted-foreground underline-offset-4 hover:text-foreground hover:underline"
              onClick={() => {
                close()
                onSites()
              }}
            >
              All sites
            </button>
          )}
        </div>
      )}
    </Pop>
  )
}

/** Ask first / Allow / Block for clicks and typing on a site. */
export function PolicyPicker({
  value,
  onChange,
}: {
  value: keyof typeof policyWords
  onChange: (p: keyof typeof policyWords) => void
}) {
  return (
    <div className="grid gap-1">
      <span className="text-muted-foreground">Clicks and typing</span>
      <div
        role="radiogroup"
        aria-label="Clicks and typing"
        className="inline-flex w-fit rounded-md border p-0.5"
      >
        {(Object.keys(policyWords) as (keyof typeof policyWords)[]).map((p) => (
          <button
            key={p}
            type="button"
            role="radio"
            aria-checked={value === p}
            onClick={() => onChange(p)}
            className={cn(
              "rounded px-2 py-0.5",
              value === p
                ? "bg-foreground text-background"
                : "text-muted-foreground hover:text-foreground"
            )}
          >
            {policyWords[p]}
          </button>
        ))}
      </div>
    </div>
  )
}

/** The files the browser saved, each sendable to one of Finn's sandboxes. */
function Downloads({
  sim,
  dispatch,
}: {
  sim: Sim
  dispatch: Dispatch<SimAction>
}) {
  const fresh = sim.downloads.filter((d) => !d.sentTo).length
  return (
    <Pop
      label="Downloads"
      align="end"
      trigger={(open, toggle) => (
        <Button
          size="icon-sm"
          variant="ghost"
          aria-label={`Downloads, ${fresh} not sent`}
          aria-expanded={open}
          className="relative"
          onClick={toggle}
        >
          <DownloadSimpleIcon />
          {fresh > 0 && (
            <span className="absolute -top-0.5 -right-0.5 inline-flex size-3.5 items-center justify-center rounded-full bg-primary font-mono text-[0.5625rem] text-primary-foreground">
              {fresh}
            </span>
          )}
        </Button>
      )}
    >
      {() => (
        <div className="grid gap-2">
          <div className="font-medium">Downloads</div>
          <ul className="grid gap-2">
            {sim.downloads.map((d) => (
              <li key={d.name} className="grid gap-1 rounded-md border p-2">
                <div className="flex items-center gap-2">
                  <span className="min-w-0 flex-1 truncate font-mono">
                    {d.name}
                  </span>
                  <span className="text-muted-foreground">
                    {d.size} · {d.when}
                  </span>
                </div>
                {d.sentTo ? (
                  <span className="flex items-center gap-1 text-muted-foreground">
                    <CheckIcon className="size-3" />
                    In <span className="font-mono">{d.sentTo}</span>
                    :/workspace/downloads
                  </span>
                ) : (
                  <span className="flex flex-wrap items-center gap-1">
                    <span className="text-muted-foreground">Send to</span>
                    {sandboxes.map((s) => (
                      <button
                        key={s}
                        type="button"
                        onClick={() =>
                          dispatch({ type: "send", file: d.name, sandbox: s })
                        }
                        className="rounded border px-1.5 py-0.5 font-mono hover:bg-muted"
                      >
                        {s}
                      </button>
                    ))}
                  </span>
                )}
              </li>
            ))}
          </ul>
          <p className="text-muted-foreground">
            Files stay in the browser VM and never open on Studio's host.
          </p>
        </div>
      )}
    </Pop>
  )
}

/** One line under the toolbar that always answers "who is driving, and what now?". */
function DriverBar({ sim, dispatch, size }: Omit<Props, "onExpand">) {
  const panel = size === "panel"
  const bar = (className: string, children: ReactNode) => (
    <div
      className={cn(
        "flex min-h-9 shrink-0 flex-wrap items-center gap-x-2 gap-y-1 border-b px-3 py-1.5 text-xs",
        className
      )}
    >
      {children}
    </div>
  )
  if (sim.vm !== "running") return null

  if (sim.replay !== null) {
    const entry = sim.log[sim.replay]
    const at = sim.replay
    return bar(
      "bg-muted/60",
      <>
        <ClockCounterClockwiseIcon className="size-3.5 shrink-0" />
        <span className="min-w-0 flex-1 truncate">
          <span className="font-medium">Replay</span>{" "}
          <span className="font-mono text-muted-foreground">{entry.time}</span>{" "}
          {entry.text}
        </span>
        <span className="flex items-center gap-0.5">
          <Button
            size="icon-xs"
            variant="ghost"
            aria-label="Previous step"
            disabled={at === 0}
            onClick={() => dispatch({ type: "replay", entry: at - 1 })}
          >
            <CaretLeftIcon />
          </Button>
          <span className="font-mono text-muted-foreground">
            {at + 1}/{sim.log.length}
          </span>
          <Button
            size="icon-xs"
            variant="ghost"
            aria-label="Next step"
            disabled={at + 1 >= sim.log.length}
            onClick={() => dispatch({ type: "replay", entry: at + 1 })}
          >
            <CaretRightIcon />
          </Button>
        </span>
        <Button
          size="xs"
          onClick={() => dispatch({ type: "replay", entry: null })}
        >
          Back to live
        </Button>
      </>
    )
  }

  if (sim.tab === "throwaway")
    return bar(
      "bg-muted/40",
      <>
        <DetectiveIcon className="size-3.5 shrink-0" />
        <span className="min-w-0 flex-1">
          {panel
            ? `Throwaway session from ${throwaway.sandbox}.`
            : `An agent in ${throwaway.sandbox} reads this in a throwaway session: a temporary profile, so nothing reaches Finn's. It ends when closed or after 10 min idle.`}
        </span>
        <Button
          size="xs"
          variant="outline"
          onClick={() => dispatch({ type: "closeThrowaway" })}
        >
          End session
        </Button>
      </>
    )

  if (sim.view.page === "held")
    return bar(
      "bg-(--st-waiting)/10",
      <>
        <ShieldWarningIcon
          className="size-3.5 shrink-0 text-(--st-waiting)"
          weight="fill"
        />
        <span className="min-w-0 flex-1">
          No rule covers{" "}
          <span className="font-mono">{hostOf(sim.view.url)}</span>.
        </span>
        <span className="flex gap-1">
          <Button
            size="xs"
            variant="outline"
            onClick={() => dispatch({ type: "denyHeld" })}
          >
            Deny
          </Button>
          <Button
            size="xs"
            variant="outline"
            onClick={() => dispatch({ type: "allowHeld", always: false })}
          >
            {panel ? "Allow now" : "Allow until it sleeps"}
          </Button>
          <Button
            size="xs"
            onClick={() => dispatch({ type: "allowHeld", always: true })}
          >
            Always allow
          </Button>
        </span>
      </>
    )

  if (sim.driver === "you")
    return bar(
      "border-b-primary/30 bg-primary/8",
      <>
        <HandIcon className="size-3.5 shrink-0 text-primary" weight="fill" />
        <span className="min-w-0 flex-1">
          <span className="font-medium">You're driving.</span>{" "}
          {!panel && "Finn's browser calls wait until you hand back."}
        </span>
        <Button
          size="xs"
          onClick={() => dispatch({ type: "wheel", to: "agent" })}
        >
          Hand back to Finn
        </Button>
      </>
    )

  const waiting = sim.pending
  return bar(
    waiting ? "bg-(--st-waiting)/10" : "",
    <>
      <span className="relative inline-flex size-4 shrink-0 items-center justify-center text-[0.75rem] leading-none">
        {teammate.avatar}
        {sim.working && (
          <span
            className={cn(
              "absolute -right-0.5 -bottom-0.5 size-1.5 rounded-full ring-1 ring-background",
              waiting ? "bg-(--st-waiting)" : "animate-pulse bg-(--st-running)"
            )}
          />
        )}
      </span>
      <span className="min-w-0 flex-1 truncate">
        {waiting ? (
          <>
            <span className="font-medium">Finn is waiting for you</span>
            {!panel && (
              <span className="text-muted-foreground">
                {" "}
                · answer on the page
              </span>
            )}
          </>
        ) : sim.working ? (
          <>
            <span className="font-medium">Finn is driving</span>
            <span className="text-muted-foreground">
              {panel ? "" : ` · ${task.title} · from `}
            </span>
            {!panel && (
              <span className="font-mono text-muted-foreground">
                {task.sandbox}
              </span>
            )}
          </>
        ) : (
          <span className="text-muted-foreground">
            Finn isn't using the browser.
            {!panel && " It sleeps after 10 min idle."}
          </span>
        )}
      </span>
      <Button
        size="xs"
        variant="outline"
        onClick={() => dispatch({ type: "wheel", to: "you" })}
      >
        <HandIcon />
        Take the wheel
      </Button>
    </>
  )
}

type Rect = { x: number; y: number; w: number; h: number }

/** The page, scaled to fit like the live canvas, with Finn's cursor and its questions on top. */
function Viewport({ sim, dispatch, size }: Omit<Props, "onExpand">) {
  const outer = useRef<HTMLDivElement>(null)
  const site = useRef<HTMLDivElement>(null)
  const [box, setBox] = useState({ w: 0, h: 0 })
  useLayoutEffect(() => {
    const el = outer.current
    if (!el) return
    const ro = new ResizeObserver(() =>
      setBox({ w: el.clientWidth, h: el.clientHeight })
    )
    ro.observe(el)
    return () => ro.disconnect()
  }, [])
  const pad = size === "panel" ? 0 : 16
  // Narrow, the page fits the width and Finn's question goes under it instead of over it.
  const narrow = box.w - pad * 2 < 560
  const scale = Math.max(
    0.1,
    narrow
      ? (box.w - pad * 2) / pageWidth
      : Math.min((box.w - pad * 2) / pageWidth, (box.h - pad * 2) / pageHeight)
  )
  const view = shownView(sim)
  const live = sim.replay === null && sim.tab === "profile"
  const target = live ? agentTarget(sim) : undefined
  const [rect, setRect] = useState<Rect>()
  useLayoutEffect(() => {
    const root = site.current
    const el =
      target && root?.querySelector<HTMLElement>(`[data-target="${target}"]`)
    if (!root || !el) {
      setRect(undefined)
      return
    }
    // The page's own box: the wrapper's layout width is already scaled once.
    const page = root.firstElementChild ?? root
    const a = page.getBoundingClientRect()
    const b = el.getBoundingClientRect()
    const s = a.width / pageWidth
    setRect({
      x: (b.left - a.left) / s,
      y: (b.top - a.top) / s,
      w: b.width / s,
      h: b.height / s,
    })
  }, [target, view, scale])
  const watching = live && sim.driver === "agent" && sim.vm === "running"
  const ask = sim.pending ? script[sim.next]?.ask : undefined
  const w = pageWidth * scale
  const h = pageHeight * scale

  return (
    <div
      ref={outer}
      className={cn(
        "relative flex min-h-0 flex-1 flex-col items-center gap-2 bg-muted/50",
        narrow ? "overflow-y-auto" : "overflow-hidden"
      )}
      style={{ padding: pad }}
    >
      {sim.vm !== "running" ? (
        <Asleep sim={sim} dispatch={dispatch} scale={scale} />
      ) : (
        <div
          className={cn(
            "group relative shrink-0 overflow-hidden bg-white",
            size === "full" && "rounded-md shadow-sm ring-1 ring-border",
            live && sim.driver === "you" && "ring-2 ring-primary"
          )}
          style={{ width: w, height: h }}
        >
          <div
            ref={site}
            className={cn(
              "origin-top-left",
              !(live && sim.driver === "you") && "pointer-events-none",
              sim.replay !== null && "grayscale-[.35]"
            )}
            style={{ transform: `scale(${scale})` }}
          >
            <FakeSite
              view={view}
              onTarget={(t) => dispatch({ type: "target", target: t })}
            />
          </div>

          {rect && (
            <>
              {ask && (
                <span
                  className="pointer-events-none absolute animate-pulse rounded-md ring-3 ring-(--st-waiting)"
                  style={{
                    left: rect.x * scale - 4,
                    top: rect.y * scale - 4,
                    width: rect.w * scale + 8,
                    height: rect.h * scale + 8,
                  }}
                />
              )}
              <AgentCursor
                x={(rect.x + rect.w * 0.55) * scale}
                y={(rect.y + rect.h * 0.6) * scale}
              />
            </>
          )}

          {ask && rect && !narrow && (
            <AskCard
              ask={ask}
              dispatch={dispatch}
              style={{
                left: Math.min(Math.max(8, rect.x * scale), w - 328),
                top:
                  (rect.y + rect.h) * scale + 176 < h
                    ? (rect.y + rect.h) * scale + 12
                    : Math.max(8, rect.y * scale - 172),
              }}
            />
          )}

          {watching && !ask && (
            <div className="absolute inset-0 flex items-end justify-center bg-black/0 pb-4 opacity-0 transition group-hover:bg-black/5 group-hover:opacity-100 focus-within:opacity-100">
              <Button
                size="sm"
                variant="outline"
                className="bg-background/95 shadow-md"
                onClick={() => dispatch({ type: "wheel", to: "you" })}
              >
                <HandIcon />
                Watching Finn · take the wheel to use the page
              </Button>
            </div>
          )}
        </div>
      )}
      {sim.vm === "running" && ask && narrow && (
        <AskCard ask={ask} dispatch={dispatch} />
      )}
    </div>
  )
}

/** Finn's pointer: it glides to the element of the step it is on. */
function AgentCursor({ x, y }: { x: number; y: number }) {
  return (
    <span
      aria-hidden
      className="pointer-events-none absolute z-10 transition-[left,top] duration-700 ease-out"
      style={{ left: x, top: y }}
    >
      <NavigationArrowIcon
        weight="fill"
        className="size-4 -scale-x-100 text-(--acc-chat) drop-shadow"
      />
      <span className="absolute top-3.5 left-3 rounded-full bg-(--acc-chat) px-1.5 py-px text-[0.625rem] font-medium whitespace-nowrap text-white shadow">
        Finn
      </span>
    </span>
  )
}

/**
 * The approval, on the page next to what it is about. The approval stack shows the same
 * request elsewhere in the app; answering either answers both.
 */
function AskCard({
  ask,
  dispatch,
  style,
}: {
  ask: Ask
  dispatch: Dispatch<SimAction>
  /** Where it floats over the page; without it, it sits under the page. */
  style?: CSSProperties
}) {
  const decide = (how: "allow" | "always" | "deny") =>
    dispatch({ type: "decide", how })
  const icon =
    ask.kind === "credential"
      ? KeyIcon
      : ask.kind === "download"
        ? DownloadSimpleIcon
        : CursorClickIcon
  const Icon = icon
  return (
    <div
      role="alertdialog"
      aria-label="Finn is asking"
      className={cn(
        "z-20 grid shrink-0 animate-[tm-in_.25s_ease-out] gap-2 rounded-lg border border-l-3 bg-popover p-3 text-xs text-popover-foreground shadow-xl",
        ruleClass("browser"),
        style ? "absolute w-80" : "mx-2 w-[calc(100%-1rem)]"
      )}
      style={style}
    >
      <div className="flex items-center gap-2">
        <span
          className={cn(
            "inline-flex size-6 shrink-0 items-center justify-center rounded",
            tileClass("browser")
          )}
        >
          <Icon className="size-3.5" weight="bold" />
        </span>
        <KindChip>
          {ask.kind === "credential"
            ? "Login"
            : ask.kind === "download"
              ? "Download"
              : "Page action"}
        </KindChip>
        <span className="ml-auto text-muted-foreground">just now</span>
      </div>
      <p className="text-[0.8125rem]">
        {ask.kind === "credential" ? (
          <>
            Finn wants to fill <b>{ask.field}</b> with{" "}
            <span className="font-mono">{ask.secret}</span>.
          </>
        ) : ask.kind === "download" ? (
          <>
            Finn wants to download <span className="font-mono">{ask.file}</span>{" "}
            ({ask.size}).
          </>
        ) : (
          <>
            Finn wants to {ask.verb} <b>“{ask.name}”</b>.
          </>
        )}
      </p>
      <p className="text-muted-foreground">
        {ask.kind === "credential"
          ? "Studio types it in. Finn only ever sees the name."
          : ask.kind === "download"
            ? "It stays in the browser until you send it to a sandbox."
            : "Clicks and typing on console.pebble.dev ask first."}
      </p>
      <div className="flex flex-wrap items-center justify-end gap-1.5">
        <Button size="sm" variant="ghost" onClick={() => decide("deny")}>
          Deny
        </Button>
        {ask.kind === "action" && (
          <Button size="sm" variant="outline" onClick={() => decide("always")}>
            Always on this site
          </Button>
        )}
        <Button size="sm" onClick={() => decide("allow")}>
          {ask.kind === "credential"
            ? "Fill"
            : ask.kind === "download"
              ? "Download"
              : "Allow once"}
        </Button>
      </div>
    </div>
  )
}

/** The asleep browser: its last page, faded, and a way to open a page right there. */
function Asleep({
  sim,
  dispatch,
  scale,
}: {
  sim: Sim
  dispatch: Dispatch<SimAction>
  scale: number
}) {
  const [url, setUrl] = useState("")
  return (
    <div className="relative flex size-full items-center justify-center">
      <div
        aria-hidden
        className="pointer-events-none absolute top-0 overflow-hidden rounded-md opacity-25 blur-[1px] grayscale"
        style={{ width: pageWidth * scale, height: pageHeight * scale }}
      >
        <div
          className="origin-top-left"
          style={{ transform: `scale(${scale})` }}
        >
          <FakeSite view={sim.view} />
        </div>
      </div>
      <div className="relative grid w-full max-w-sm gap-3 rounded-xl border bg-background/95 p-5 text-center shadow-lg">
        {sim.vm === "waking" ? (
          <>
            <Spinner className="mx-auto" />
            <p className="text-sm font-medium">Waking Finn's browser…</p>
            <p className="text-xs text-muted-foreground">
              It reopens {hostOf(sim.view.url)}, signed in.
            </p>
          </>
        ) : (
          <>
            <span
              className={cn(
                "mx-auto inline-flex size-9 items-center justify-center rounded-lg",
                tileClass("browser")
              )}
            >
              <MoonIcon className="size-4.5" weight="fill" />
            </span>
            <div>
              <p className="text-sm font-medium">Finn's browser is asleep</p>
              <p className="mt-1 text-xs text-muted-foreground">
                It wakes when Finn browses, or when you open a page. Logins and
                cookies are kept.
              </p>
            </div>
            <form
              className="flex gap-1.5"
              onSubmit={(e) => {
                e.preventDefault()
                dispatch({ type: "wake" })
                if (url.trim()) {
                  const target = url.trim()
                  setTimeout(
                    () => dispatch({ type: "navigate", url: target }),
                    1500
                  )
                }
              }}
            >
              <input
                aria-label="Open a page"
                placeholder="Open a page…"
                value={url}
                onChange={(e) => setUrl(e.target.value)}
                className="h-8 min-w-0 flex-1 rounded-md border bg-background px-2 font-mono text-xs outline-none focus:border-ring focus:ring-2 focus:ring-ring/30"
              />
              <Button size="sm" type="submit">
                {url.trim() ? "Open" : "Wake up"}
              </Button>
            </form>
            <p className="text-[0.6875rem] text-muted-foreground">
              Signed in on {sites.filter((s) => s.signedIn).length} sites · last
              page <span className="font-mono">{hostOf(sim.view.url)}</span>
            </p>
          </>
        )}
      </div>
    </div>
  )
}
