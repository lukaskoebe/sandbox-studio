import { useRef, useState, type ReactNode } from "react"
import {
  ArrowLineLeftIcon,
  ArrowLineRightIcon,
  ArrowsInSimpleIcon,
  ArrowsOutSimpleIcon,
  BrowserIcon,
  CaretDownIcon,
  GitDiffIcon,
  GitForkIcon,
  MonitorIcon,
  PlusIcon,
  PowerIcon,
  TerminalWindowIcon,
  WarningIcon,
  type Icon,
} from "@phosphor-icons/react"
import { BrowserRail, type RailTab } from "@/components/browser-rail"
import { BrowserWindow, type BrowserReplay } from "@/components/browser-window"
import { ChangesView } from "@/components/computer/changes"
import { ComputerMenu } from "@/components/computer/computer-menu"
import { Terminals } from "@/components/computer/terminals"
import { CreateSandboxDialog } from "@/components/create-sandbox-dialog"
import { PreviewPanel } from "@/components/chat/preview-panel"
import { isGitApproval } from "@/components/git-review"
import { MergeBanner } from "@/components/merge-fork"
import { lostVM, useSandboxLifecycle } from "@/components/sandbox-lifecycle"
import { DeleteSandboxButton } from "@/components/sandbox-options-menu"
import {
  RebaseFailedAlert,
  SandboxProgress,
} from "@/components/sandbox-progress"
import { phase, phaseDot } from "@/components/status-badge"
import { StatusDot } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Spinner } from "@/components/ui/spinner"
import { usePendingApprovals } from "@/hooks/use-pending-approvals"
import { $api, type Sandbox, type Teammate } from "@/lib/api/client"
import { isBrowserAsk } from "@/lib/approval-kinds"
import type { Chat, ChatTurn, ChatItem } from "@/lib/chats"
import type { ScreenSearch, ScreenTab } from "@/lib/computer"
import { usePreviewSetting } from "@/lib/preview-setting"
import { useTerminalTheme } from "@/lib/theme"
import { cn, formatAge } from "@/lib/utils"

/** What a screen change asks of the page: another tab, session, review, size or computer. */
export type ScreenChange = Partial<ScreenSearch> & { computer?: string }

const tabMeta: Record<ScreenTab, { label: string; icon: Icon }> = {
  terminal: { label: "Terminal", icon: TerminalWindowIcon },
  browser: { label: "Browser", icon: BrowserIcon },
  preview: { label: "Preview", icon: MonitorIcon },
  changes: { label: "Changes", icon: GitDiffIcon },
}

/**
 * A computer drawn as one screen (docs/design/teammate-computer.md): a bezel with the computer's
 * name, which switches between the owner's computers, the tabs Terminal, Browser (a teammate's
 * own), Preview and Changes, power, ⋯, enlarge and fold; under it the display. A computer that
 * is off shows a dark glass with Start; its teammate's browser, which runs in a VM of its own,
 * stays a tab beside it. Folded, the screen is a strip of tab icons.
 */
export function ComputerScreen({
  env,
  sandbox: sb,
  computers,
  owner,
  search,
  onChange,
  solo,
  enlargeable,
  lead,
  thread,
}: {
  env: string
  sandbox: Sandbox
  /** The computers the switcher offers: the owner's, or the plain ones. */
  computers: Sandbox[]
  owner?: Teammate
  search: ScreenSearch
  onChange: (change: ScreenChange) => void
  /** The screen fills the page: a plain computer, with nothing beside it to fold for. */
  solo?: boolean
  /** The screen can take the page, with the conversation a strip beside it. */
  enlargeable?: boolean
  /** Shown first on the bezel, such as the sidebar's toggle on a phone. */
  lead?: ReactNode
  /** The open thread, whose changes Changes shows. */
  thread?: {
    chat: Chat
    turns: ChatTurn[]
    items: ChatItem[]
    turnId?: string
    onTurn: (id: string) => void
  }
}) {
  const p = phase(sb)
  const previews = usePreviewSetting().enabled
  const pending = usePendingApprovals()
  const reviews = (pending.data ?? []).filter(
    (a) => a.sandboxId === sb.id && isGitApproval(a)
  )
  const browserAsks = owner
    ? (pending.data ?? []).filter(
        (a) => a.environmentId === env && isBrowserAsk(a, owner.id)
      ).length
    : 0
  const tabs: ScreenTab[] = [
    "terminal",
    ...(owner ? (["browser"] as const) : []),
    ...(previews ? (["preview"] as const) : []),
    "changes",
  ]
  const on = p === "ready"
  const shown: ScreenTab[] = on
    ? tabs
    : owner && sb.status !== "creating" && sb.status !== "failed"
      ? ["terminal", "browser"]
      : []
  const tab = shown.find((t) => t === search.tab) ?? "terminal"
  // The Terminals tab's own query: the Terminal tab counts its sessions.
  const terminals = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes/{id}/terminals",
    { params: { path: { env, id: sb.id } } },
    { refetchInterval: 5000, enabled: on }
  )
  const counts: Partial<Record<ScreenTab, number>> = {
    terminal: on ? (terminals.data?.length ?? 0) : 0,
  }
  const dots: Partial<Record<ScreenTab, boolean>> = {
    changes: reviews.length > 0,
    browser: browserAsks > 0,
  }
  // A browser replay and its activity stay while the tab is switched away and back.
  const [replay, setReplay] = useState<BrowserReplay | null>(null)
  const [rail, setRail] = useState<RailTab>()

  if (search.fold && !solo) {
    return (
      <section
        aria-label={`${sb.name}, folded`}
        className="flex w-14 shrink-0 flex-col items-center gap-1 border-l bg-sidebar py-2"
      >
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label="Unfold the screen"
          title="Unfold the screen"
          className="text-muted-foreground"
          onClick={() => onChange({ fold: false })}
        >
          <ArrowLineLeftIcon />
        </Button>
        <span className="my-1 h-px w-6 bg-border" />
        {shown.map((t) => {
          const M = tabMeta[t]
          return (
            <Button
              key={t}
              variant="ghost"
              size="icon-sm"
              aria-label={M.label}
              title={M.label}
              className="relative text-muted-foreground"
              onClick={() => onChange({ tab: t, fold: false })}
            >
              <M.icon />
              {dots[t] && (
                <span className="absolute top-1 right-1 size-1.5 rounded-full bg-(--st-waiting)" />
              )}
            </Button>
          )
        })}
        <span className="mt-auto" title={`${sb.name}: ${phaseDot(p).label}`}>
          <StatusDot {...phaseDot(p)} compact />
        </span>
      </section>
    )
  }

  return (
    <section
      aria-label={`${sb.name}'s screen`}
      className={cn(
        "flex min-h-0 min-w-0 flex-col",
        solo ? "flex-1 p-3 md:p-4" : "flex-[1.25] py-2.5 pr-3 pl-0.5"
      )}
    >
      <div className="@container flex min-h-0 flex-1 flex-col rounded-3xl border bg-sidebar px-1.5 pb-1.5 dark:bg-[#0b0f11]">
        <div className="flex h-11 shrink-0 items-center gap-2 pl-0.5">
          {lead}
          <Switcher
            env={env}
            sandbox={sb}
            computers={computers}
            owner={owner}
            onPick={(id) => onChange({ computer: id })}
          />
          {shown.length ? (
            <div
              role="tablist"
              aria-label={`${sb.name}'s screen`}
              className="mx-auto flex items-center gap-0.5 rounded-xl bg-foreground/5 p-[3px]"
            >
              {shown.map((t) => {
                const M = tabMeta[t]
                const active = t === tab
                return (
                  <button
                    key={t}
                    type="button"
                    role="tab"
                    aria-selected={active}
                    onClick={() => onChange({ tab: t })}
                    title={M.label}
                    className={cn(
                      "flex h-[26px] items-center gap-1.5 rounded-md px-2.5 text-xs whitespace-nowrap text-muted-foreground transition-colors hover:text-foreground",
                      active &&
                        "bg-background font-medium text-foreground shadow-[0_1px_2px_rgb(0_0_0/8%),0_0_0_1px_color-mix(in_oklab,var(--border)_70%,transparent)] dark:bg-accent",
                      // A narrow screen names only the shown tab; a phone's names none,
                      // to leave room for the computer's name.
                      active ? "@max-[26rem]:px-2" : "@max-[38rem]:px-2"
                    )}
                  >
                    <M.icon className="size-3.5" />
                    <span
                      className={
                        active ? "@max-[26rem]:hidden" : "@max-[38rem]:hidden"
                      }
                    >
                      {M.label}
                    </span>
                    {(counts[t] ?? 0) > 1 && (
                      <span
                        aria-label={`${counts[t]} sessions`}
                        className="rounded-sm border px-1 font-mono text-meta leading-3.5 font-normal text-muted-foreground"
                      >
                        {counts[t]}
                      </span>
                    )}
                    {dots[t] && (
                      <span
                        aria-label="waiting for you"
                        className="size-1.5 rounded-full bg-(--st-waiting)"
                      />
                    )}
                  </button>
                )
              })}
            </div>
          ) : (
            <span className="mx-auto" />
          )}
          <div className="flex items-center gap-px">
            <Power env={env} sandbox={sb} />
            <ComputerMenu env={env} sandbox={sb} owner={owner} />
            {enlargeable && (
              <Button
                variant="ghost"
                size="icon-sm"
                aria-label={
                  search.full ? "Shrink the screen" : "Enlarge the screen"
                }
                title={search.full ? "Shrink the screen" : "Enlarge the screen"}
                className="text-muted-foreground hover:text-foreground"
                onClick={() => onChange({ full: !search.full })}
              >
                {search.full ? <ArrowsInSimpleIcon /> : <ArrowsOutSimpleIcon />}
              </Button>
            )}
            {!solo && (
              <Button
                variant="ghost"
                size="icon-sm"
                aria-label="Fold the screen"
                title="Fold the screen"
                className="text-muted-foreground hover:text-foreground"
                onClick={() => onChange({ fold: true })}
              >
                <ArrowLineRightIcon />
              </Button>
            )}
          </div>
        </div>
        <MergeBanner env={env} sandbox={sb} />
        <RebaseFailedAlert env={env} sandbox={sb} />
        <Display
          off={!on && tab !== "browser"}
          terminal={on && tab === "terminal"}
        >
          {sb.status === "creating" || sb.status === "failed" ? (
            <SandboxProgress
              sandbox={sb}
              action={
                sb.status === "failed" ? (
                  <DeleteSandboxButton env={env} sandbox={sb} />
                ) : undefined
              }
            />
          ) : !on && tab !== "browser" ? (
            <Off env={env} sandbox={sb} />
          ) : (
            <>
              {/* Terminals stay mounted while another tab shows. */}
              {on && (
                <Terminals
                  key={sb.id}
                  env={env}
                  sandbox={sb}
                  term={search.term}
                  visible={tab === "terminal"}
                  suspended={false}
                  onTerm={(term) => onChange({ tab: "terminal", term })}
                />
              )}
              {tab === "browser" && owner && (
                <BrowserTab
                  key={owner.id}
                  env={env}
                  owner={owner}
                  rail={rail}
                  onRail={setRail}
                  replay={replay}
                  onReplay={setReplay}
                />
              )}
              {tab === "preview" && (
                <div className="flex min-h-0 flex-1 flex-col p-3">
                  <PreviewPanel
                    env={env}
                    sandboxId={sb.id}
                    reloadKey={thread?.turns.filter((t) => t.endedAt).length}
                  />
                </div>
              )}
              {tab === "changes" && (
                <ChangesView
                  env={env}
                  sandbox={sb}
                  reviews={reviews}
                  review={search.review}
                  onReview={(review) => onChange({ review })}
                  thread={thread}
                />
              )}
            </>
          )}
        </Display>
      </div>
    </section>
  )
}

/** How wide the Browser tab must be for its activity to sit beside the page, in rem. */
const railBeside = 46

/**
 * The teammate's browser, and its activity and sites beside it, or over it on a narrow screen,
 * where picking a step to replay closes them so the page shows.
 */
function BrowserTab({
  env,
  owner,
  rail,
  onRail,
  replay,
  onReplay,
}: {
  env: string
  owner: Teammate
  rail?: RailTab
  onRail: (rail?: RailTab) => void
  replay: BrowserReplay | null
  onReplay: (replay: BrowserReplay | null) => void
}) {
  const box = useRef<HTMLDivElement>(null)
  const pick = (r: BrowserReplay | null) => {
    onReplay(r)
    const rem = parseFloat(getComputedStyle(document.documentElement).fontSize)
    if (r && (box.current?.clientWidth ?? 0) < railBeside * rem) onRail()
  }
  return (
    <div ref={box} className="@container/browser relative flex min-h-0 flex-1">
      <BrowserWindow
        env={env}
        teammate={owner.id}
        activity={!!rail}
        onActivity={() => onRail(rail ? undefined : "activity")}
        onSites={() => onRail("sites")}
        replay={replay}
        onReplay={onReplay}
      />
      {rail && (
        <BrowserRail
          env={env}
          teammate={owner.id}
          name={owner.name}
          tab={rail}
          onTab={onRail}
          onClose={() => onRail()}
          replay={replay}
          onReplay={pick}
          className="@max-[46rem]/browser:absolute @max-[46rem]/browser:inset-y-0 @max-[46rem]/browser:right-0 @max-[46rem]/browser:z-30 @max-[46rem]/browser:max-w-[85%] @max-[46rem]/browser:shadow-lg"
        />
      )}
    </div>
  )
}

function Display({
  off,
  terminal,
  children,
}: {
  off: boolean
  terminal: boolean
  children: ReactNode
}) {
  // The terminal follows the page's theme unless it is kept dark; the display goes with it.
  const dark = useTerminalTheme() === "dark"
  return (
    <div
      className={cn(
        "relative flex min-h-0 flex-1 flex-col overflow-hidden rounded-xl border",
        off
          ? "border-(--glass-edge) bg-(image:--glass)"
          : terminal
            ? dark
              ? "bg-(--wb-term-bg)"
              : "bg-[#fbfcfc]"
            : "bg-card"
      )}
    >
      {children}
    </div>
  )
}

/**
 * The computer's name on the bezel, with its status dot: it switches between the computers it
 * lists, forks under the one they came from, and adds another for a teammate.
 */
function Switcher({
  env,
  sandbox: sb,
  computers,
  owner,
  onPick,
}: {
  env: string
  sandbox: Sandbox
  computers: Sandbox[]
  owner?: Teammate
  onPick: (id: string) => void
}) {
  const [adding, setAdding] = useState(false)
  // Forks follow the computer they came from.
  const ordered: Sandbox[] = []
  const ids = new Set(computers.map((c) => c.id))
  const visit = (c: Sandbox) => {
    ordered.push(c)
    for (const f of computers.filter((x) => x.forkOf === c.id)) visit(f)
  }
  for (const c of computers.filter((x) => !x.forkOf || !ids.has(x.forkOf)))
    visit(c)
  const p = phase(sb)
  if (computers.length <= 1 && !owner)
    return (
      <span className="flex h-[30px] min-w-0 items-center gap-2 px-2">
        <StatusDot {...phaseDot(p)} compact />
        <span className="truncate font-mono text-title font-semibold">
          {sb.name}
        </span>
      </span>
    )
  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger
          title={`${sb.name}: ${phaseDot(p).label}`}
          className="flex h-[30px] min-w-0 items-center gap-2 rounded-md px-2 hover:bg-sidebar-accent data-popup-open:bg-sidebar-accent"
        >
          <StatusDot {...phaseDot(p)} compact />
          <span className="truncate font-mono text-title font-semibold">
            {sb.name}
          </span>
          <CaretDownIcon className="size-3 shrink-0 text-muted-foreground" />
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start" className="w-64">
          <DropdownMenuGroup>
            <DropdownMenuLabel>
              {owner ? `${owner.name}'s computers` : "Your computers"}
            </DropdownMenuLabel>
            {ordered.map((c) => (
              <DropdownMenuItem
                key={c.id}
                aria-current={c.id === sb.id || undefined}
                className={cn(c.forkOf && ids.has(c.forkOf) && "pl-5")}
                onClick={() => onPick(c.id)}
              >
                {c.forkOf && ids.has(c.forkOf) ? (
                  <GitForkIcon className="text-muted-foreground" />
                ) : (
                  <StatusDot {...phaseDot(phase(c))} compact />
                )}
                <span className="min-w-0 truncate font-mono">{c.name}</span>
                <span className="ml-auto text-caption text-muted-foreground">
                  {phaseDot(phase(c)).label}
                </span>
              </DropdownMenuItem>
            ))}
          </DropdownMenuGroup>
          {owner && (
            <>
              <DropdownMenuSeparator />
              <DropdownMenuItem onClick={() => setAdding(true)}>
                <PlusIcon />
                Another computer for {owner.name}
              </DropdownMenuItem>
            </>
          )}
        </DropdownMenuContent>
      </DropdownMenu>
      {owner && (
        <CreateSandboxDialog
          env={env}
          teammate={owner.id}
          open={adding}
          onOpenChange={setAdding}
        />
      )}
    </>
  )
}

/** The bezel's power button: Stop while the computer runs, Start or Resume while it is down. */
function Power({ env, sandbox: sb }: { env: string; sandbox: Sandbox }) {
  const { path, start, stop, resume, phase: p } = useSandboxLifecycle(env, sb)
  if (sb.status === "creating" || sb.status === "failed") return null
  const up = p === "ready" || p === "booting"
  const mutation = up ? stop : p === "suspended" ? resume : start
  const label = up
    ? `Stop ${sb.name}; its disk is kept`
    : p === "suspended"
      ? `Resume ${sb.name}`
      : `Start ${sb.name}`
  return (
    <Button
      variant="ghost"
      size="icon-sm"
      aria-label={label}
      title={label}
      disabled={mutation.isPending || sb.status === "absent"}
      className={cn(
        "text-muted-foreground hover:text-foreground",
        !up && "text-(--st-running) hover:text-(--st-running)"
      )}
      onClick={() => mutation.mutate(path)}
    >
      {mutation.isPending ? <Spinner /> : <PowerIcon />}
    </Button>
  )
}

/** A computer that is off: blank glass, what is kept, and the button that brings it back. */
function Off({ env, sandbox: sb }: { env: string; sandbox: Sandbox }) {
  const { path, start, resume, phase: p } = useSandboxLifecycle(env, sb)
  const checkpoints = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes/{id}/checkpoints",
    { params: { path: { env, id: sb.id } } }
  )
  const latest = [...(checkpoints.data ?? [])]
    .filter((c) => c.state === "ready")
    .sort((a, b) => b.createdAt.localeCompare(a.createdAt))
    .at(0)
  const lost = sb.status === "absent"
  const crashed = p === "failed"
  const mutation = p === "suspended" ? resume : start
  return (
    <div className="m-auto grid max-w-80 justify-items-center gap-2.5 p-5 text-center">
      <span className="grid size-11 place-items-center rounded-full border border-(--glass-ring) text-(--glass-dim)">
        {p === "booting" ? (
          <Spinner />
        ) : lost || crashed ? (
          <WarningIcon className="size-5" />
        ) : (
          <PowerIcon className="size-5" />
        )}
      </span>
      <b className="text-sm font-semibold text-(--glass-fg)">
        <span className="font-mono">{sb.name}</span>{" "}
        {p === "booting"
          ? "is starting…"
          : p === "suspended"
            ? "is suspended"
            : lost
              ? "can't start"
              : crashed
                ? "stopped unexpectedly"
                : "is off"}
      </b>
      <p className="text-xs text-(--glass-dim)">
        {lost
          ? lostVM
          : p === "booting"
            ? "Its terminals open once it is up."
            : p === "suspended"
              ? "Resume it to continue where it left off."
              : "Its workspace, home directory and Docker images are kept."}
        {latest && !lost && p !== "booting" && (
          <>
            {" "}
            Last checkpoint{" "}
            <span className="font-mono text-(--glass-fg)">
              {latest.name}
            </span>, {formatAge(latest.createdAt)}.
          </>
        )}
      </p>
      {!lost && p !== "booting" && (
        <Button
          className="mt-1"
          disabled={mutation.isPending}
          onClick={() => mutation.mutate(path)}
        >
          {mutation.isPending ? <Spinner /> : <PowerIcon />}
          {p === "suspended" ? "Resume" : "Start"}
        </Button>
      )}
    </div>
  )
}
