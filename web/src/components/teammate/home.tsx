import { useRef, useState, type ComponentProps, type ReactNode } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { Link } from "@tanstack/react-router"
import { ThreadDot } from "@/components/thread-status"
import { toast } from "sonner"
import {
  BrainIcon,
  BrowserIcon,
  CaretDownIcon,
  ChatCircleDotsIcon,
  ChatCircleIcon,
  CheckCircleIcon,
  ClockIcon,
  FolderIcon,
  ListChecksIcon,
  PlusIcon,
  PowerIcon,
  SparkleIcon,
  TerminalWindowIcon,
  type Icon,
} from "@phosphor-icons/react"
import { BaseMark } from "@/components/base-dialog"
import { CreateSandboxDialog } from "@/components/create-sandbox-dialog"
import { phase, phaseDot } from "@/components/status-badge"
import {
  useSandboxLifecycle,
  useWakeOnType,
  wakeAction,
} from "@/components/sandbox-lifecycle"
import { StatusDot } from "@/components/status-dot"
import { NeedsYouList, useApprovalReview } from "@/components/needs-you"
import { BrowsingCard, useBrowsing } from "@/components/browser-browsing"
import { isGitApproval } from "@/components/git-review"
import { ChatContextMenu } from "@/components/chat/chat-actions"
import { ComposerDock } from "@/components/chat/chat-thread"
import { Composer, type Prompt } from "@/components/chat/composer"
import { handFirstMessage } from "@/lib/first-message"
import { ModelPicker } from "@/components/chat/model-picker"
import { usePendingApprovals } from "@/hooks/use-pending-approvals"
import {
  activityDetail,
  presenceLabels,
  useActivity,
  type Presence,
} from "@/hooks/use-teammate-presence"
import { InTopBar } from "@/components/top-bar-slot"
import { PresenceDot } from "@/components/teammate-avatar"
import { Readiness } from "@/components/teammate-readiness"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Spinner } from "@/components/ui/spinner"
import {
  $api,
  errorMessage,
  fetchClient,
  type Approval,
  type Sandbox,
  type Teammate,
} from "@/lib/api/client"
import type { components } from "@/lib/api/schema.gen"
import { useClaimApprovals } from "@/lib/approval-claims"
import { approvalKind, isBrowserAsk } from "@/lib/approval-kinds"
import { hasNews, useChatsSeen } from "@/lib/chat-seen"
import { chatTitle, useChat, type Chat } from "@/lib/chats"
import type { ScreenTab } from "@/lib/computer"
import {
  agentTerminals,
  chatApprovals,
  chatStatus,
  statusLabels,
  type AgentTerminal,
  type ThreadStatus,
} from "@/lib/threads"
import { cn } from "@/lib/utils"
import { useNewThreadFocus } from "@/lib/new-thread"

type Event = components["schemas"]["TeammateEvent"]
type Turn = components["schemas"]["TeammateTurn"]
type Mode = "message" | "task" | "teach"

const modes: Record<Mode, { label: string; icon: Icon }> = {
  message: { label: "Thread", icon: ChatCircleDotsIcon },
  task: { label: "Task", icon: ListChecksIcon },
  teach: { label: "Teach", icon: BrainIcon },
}

const timeFormat = new Intl.DateTimeFormat(undefined, {
  hour: "2-digit",
  minute: "2-digit",
})

export function time(iso: string) {
  return timeFormat.format(new Date(iso))
}

function dayLabel(d: Date, now = new Date()) {
  const day = (x: Date) =>
    new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime()
  const diff = Math.round((day(now) - day(d)) / 86400000)
  if (diff === 0) return "Today"
  if (diff === 1) return "Yesterday"
  if (diff < 7) return d.toLocaleDateString(undefined, { weekday: "long" })
  return d.toLocaleDateString(undefined, { month: "long", day: "numeric" })
}

/** How long ago, as short as a card's corner wants it: now, 12m, 5h, Mon, Oct 3. */
function ago(iso: string, now = Date.now()) {
  const minutes = Math.floor((now - Date.parse(iso)) / 60000)
  if (minutes < 1) return "now"
  if (minutes < 60) return `${minutes}m`
  if (minutes < 24 * 60) return `${Math.floor(minutes / 60)}h`
  const d = new Date(iso)
  if (minutes < 7 * 24 * 60)
    return d.toLocaleDateString(undefined, { weekday: "short" })
  return d.toLocaleDateString(undefined, { month: "short", day: "numeric" })
}

/** The approvals that wait on this teammate: its computers', its chats', its memory's. */
export function teammateApprovals(
  list: Approval[],
  env: string,
  teammate: string,
  sandboxIds: Set<string>
) {
  return list.filter(
    (a) =>
      a.environmentId === env &&
      (a.teammate?.id === teammate ||
        a.memoryShare?.teammateId === teammate ||
        a.browserAction?.teammateId === teammate ||
        a.browserCredential?.teammateId === teammate ||
        a.browserControl?.teammateId === teammate ||
        (a.sandboxId !== undefined && sandboxIds.has(a.sandboxId)))
  )
}

/** Threads untouched for longer than this wait behind "Earlier threads". */
const recentDays = 14

type HomeProps = {
  env: string
  teammate: Teammate
  /** The teammate's chats: its threads. */
  chats: Chat[]
  computers: Sandbox[]
  /** The computer on the screen, where a new thread starts. */
  computer?: Sandbox
  waiting: Approval[]
  /** Whether the screen shows the browser, whose questions then show there. */
  watching: boolean
  /** The desk: the home narrowed to a panel beside the open computer. */
  compact?: boolean
  /** Whether the computers have loaded, so "no computer yet" is true. */
  loaded: boolean
  onThread: (chat: Chat) => void
  onProfile: (view: "tasks" | "memory" | "settings") => void
  onWatch: () => void
  /** Opens a push waiting for review on the screen's Changes. */
  onReview: (a: Approval) => void
  /** Opens one of its computers on the screen, on a tab and terminal session. */
  onOpenComputer: (c: Sandbox, tab?: ScreenTab, term?: string) => void
  /** The desk's home takes the page again, the computer folded to its rail. */
  onExpand: () => void
}

/**
 * The teammate's home (docs/design/teammate-computer.md): with the page to itself, its threads
 * by day beside its computers; on the desk, the same in brief, as a panel beside the computer.
 */
export function TeammateHome(props: HomeProps) {
  return props.compact ? <DeskHome {...props} /> : <FullHome {...props} />
}

/** The teammate's agents working in terminals of its computers with no chat behind them. */
function useAgentTerminals(env: string, computers: Sandbox[]) {
  const terminals = $api.useQuery("get", "/api/environments/{env}/terminals", {
    params: { path: { env } },
  })
  return agentTerminals(terminals.data, new Set(computers.map((c) => c.id)))
}

/**
 * The home with the page to itself (docs/design/teammate-computer.md). It opens with what the
 * teammate does now: what waits for you and what runs. Below are its threads as
 * cards by day, newest first, with what it learned and its tasks between them, and its
 * computers, each one click from its terminal, browser and files: beside the threads on a wide
 * page, above them on a narrow one. The composer at the bottom starts a new thread.
 */
function FullHome({
  env,
  teammate,
  chats,
  computers,
  computer,
  waiting,
  watching,
  loaded,
  onThread,
  onProfile,
  onWatch,
  onReview,
  onOpenComputer,
}: HomeProps) {
  const id = teammate.id
  const running = chats.find((c) => c.state === "running")
  const timeline = $api.useQuery(
    "get",
    "/api/environments/{env}/teammates/{id}/timeline",
    { params: { path: { env, id } } },
    { refetchInterval: running ? 4000 : 30000 }
  )
  const live = useChat(env, running?.id)
  const browsing = useBrowsing(env, id).now
  const browserWaiting = waiting.some((a) => isBrowserAsk(a, id))
  const shownWaiting = watching
    ? waiting.filter((a) => !isBrowserAsk(a, id))
    : waiting
  const [older, setOlder] = useState(false)
  const seen = useChatsSeen()

  const events = timeline.data ?? []
  const turns = new Map<string, Turn[]>()
  for (const e of events) {
    if (!e.turn) continue
    turns.set(e.turn.chatId, [...(turns.get(e.turn.chatId) ?? []), e.turn])
  }
  const cutoff = Date.now() - recentDays * 86400000
  const sorted = [...chats].sort((a, b) =>
    a.updatedAt.localeCompare(b.updatedAt)
  )
  const recent = sorted.filter((c) => Date.parse(c.updatedAt) >= cutoff)
  // With nothing recent, the last few threads still show.
  const shown = older ? sorted : recent.length > 0 ? recent : sorted.slice(-5)
  const hidden = sorted.length - shown.length

  type Entry = { at: string; key: string; node: ReactNode }
  const entries: Entry[] = [
    ...shown.map((chat) => ({
      at: chat.updatedAt,
      key: `chat-${chat.id}`,
      node: (
        <ThreadCard
          chat={chat}
          asks={chatApprovals(chat, waiting)}
          turns={turns.get(chat.id) ?? []}
          computer={
            computers.length > 1
              ? computers.find((s) => s.id === chat.sandboxId)?.name
              : undefined
          }
          live={
            chat.id === running?.id && live.chat?.id === chat.id
              ? live
              : undefined
          }
          news={hasNews(chat, seen)}
          onOpen={() => onThread(chat)}
        />
      ),
    })),
    ...events.flatMap((e) =>
      e.turn
        ? []
        : [
            {
              at: e.at,
              key: `${e.kind}-${e.fact?.id ?? e.task?.id ?? e.session?.id}-${e.task?.event ?? ""}`,
              node: (
                <SmallEvent
                  env={env}
                  event={e}
                  onTasks={() => onProfile("tasks")}
                />
              ),
            },
          ]
    ),
  ].sort((a, b) => b.at.localeCompare(a.at))

  const rows: ReactNode[] = []
  let day = ""
  for (const e of entries) {
    const d = new Date(e.at)
    if (d.toDateString() !== day) {
      day = d.toDateString()
      rows.push(<DayDivider key={`day-${e.at}`} label={dayLabel(d)} />)
    }
    rows.push(<div key={e.key}>{e.node}</div>)
  }
  const empty = timeline.data && chats.length === 0 && events.length === 0
  const inTerminals = useAgentTerminals(env, computers)
  const cards = (
    <ComputerCards
      env={env}
      teammate={teammate}
      computers={computers}
      loaded={loaded}
      onOpen={onOpenComputer}
    />
  )

  return (
    <div className="@container flex min-h-0 flex-1 flex-col">
      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto grid w-full max-w-5xl gap-4 px-4 pt-4 pb-6">
          <HomeStatus
            env={env}
            teammate={teammate}
            chats={chats}
            quiet={inTerminals.length > 0 || !!browsing}
          />
          {shownWaiting.length > 0 && (
            <Waiting approvals={shownWaiting} onReview={onReview} />
          )}
          <NowCard
            teammate={teammate}
            env={env}
            computers={computers}
            terminals={inTerminals}
            browsing={!!browsing}
            browserWaiting={browserWaiting}
            watching={watching}
            onWatch={onWatch}
            onOpenComputer={onOpenComputer}
          />
          <div className="grid items-start gap-4 @4xl:grid-cols-[minmax(0,1fr)_17rem] @4xl:gap-6">
            <aside
              aria-label={`${teammate.name}'s computers`}
              className="min-w-0 @4xl:col-start-2 @4xl:row-start-1"
            >
              {cards}
            </aside>
            <section
              aria-label={`Threads with ${teammate.name}`}
              className="grid min-w-0 gap-2.5 @4xl:col-start-1 @4xl:row-start-1"
            >
              {timeline.isPending && <Spinner className="mx-auto my-6" />}
              {timeline.error && (
                <p role="alert" className="text-xs text-destructive">
                  {errorMessage(timeline.error)}
                </p>
              )}
              {empty && (
                <div className="mx-auto grid max-w-xs justify-items-center gap-2 py-6 text-center">
                  <ChatCircleIcon className="size-6 text-muted-foreground" />
                  <p className="text-sm font-medium">
                    This is where you and {teammate.name} talk.
                  </p>
                  <p className="text-xs text-muted-foreground">
                    {computer
                      ? `Ask ${teammate.name} something and it starts a thread here.`
                      : `Give ${teammate.name} a computer, then ask it something: each request starts a thread here.`}
                  </p>
                </div>
              )}
              {rows}
              {hidden > 0 && (
                <Button
                  variant="ghost"
                  size="xs"
                  className="justify-self-center text-muted-foreground"
                  onClick={() => setOlder(true)}
                >
                  {hidden} earlier {hidden === 1 ? "thread" : "threads"}
                </Button>
              )}
            </section>
          </div>
        </div>
      </div>
      <NewThread
        env={env}
        teammate={teammate}
        computer={computer}
        computers={computers}
        onThread={onThread}
      />
    </div>
  )
}

/**
 * What the teammate does, in the top bar after its name: working on its chats, its agent at
 * work in a terminal, idle since when, or offline, and what keeps it from working. What waits
 * for you is Needs you's to say and the threads at work are the cards below; while the Now
 * card lists its agents in terminals or its browser, the line leaves those to it.
 */
function HomeStatus({
  env,
  teammate,
  chats,
  quiet,
}: {
  env: string
  teammate: Teammate
  chats: Chat[]
  quiet: boolean
}) {
  const providers = $api.useQuery("get", "/api/environments/{env}/providers", {
    params: { path: { env } },
  })
  const activity = useActivity(env)(teammate.id)
  const running = chats.filter((c) => c.state === "running").length
  const presence: Presence =
    running > 0
      ? "working"
      : activity.presence === "waiting"
        ? "idle"
        : activity.presence
  const last = chats.reduce<string | undefined>(
    (m, c) => (!m || c.updatedAt > m ? c.updatedAt : m),
    undefined
  )
  const since =
    presence === "idle" &&
    last &&
    new Date(last).toDateString() === new Date().toDateString()
      ? ` since ${time(last)}`
      : ""
  const terminal = running > 0 || quiet ? undefined : activity.terminal
  const detail = terminal && activityDetail({ presence, terminal })
  const label =
    running > 0
      ? `working on ${running} ${running === 1 ? "chat" : "chats"}`
      : `${presenceLabels[presence].toLowerCase()}${since}`
  return (
    <InTopBar slot="status">
      <span
        role="group"
        aria-label={`${teammate.name} now`}
        className="flex min-w-0 items-center gap-1.5 text-xs text-muted-foreground"
      >
        <PresenceDot
          presence={presence}
          busy={activity.terminal?.state === "busy"}
        />
        {/* A narrow window keeps the dot; the words are in its title. */}
        <span className="min-w-0 truncate max-md:sr-only">
          {label}
          {terminal && detail && (
            <>
              {" · "}
              <Link
                to="/e/$env/teammates/$id"
                params={{ env, id: teammate.id }}
                search={(prev) => ({
                  ...prev,
                  tab: "terminal" as const,
                  term: terminal.name,
                  computer: terminal.sandboxId,
                })}
                className="text-foreground underline-offset-2 hover:underline"
              >
                {detail}
              </Link>
            </>
          )}
        </span>
        <Readiness
          teammate={teammate}
          provider={providers.data?.find((p) => p.id === teammate.providerId)}
          problemsOnly
        />
      </span>
    </InTopBar>
  )
}

/**
 * What goes on outside the teammate's threads, under Needs you: its agents in terminals and its
 * browser. Its threads at work are the cards below, and its status is in the top bar.
 */
function NowCard({
  env,
  teammate,
  computers,
  terminals,
  browsing,
  browserWaiting,
  watching,
  onWatch,
  onOpenComputer,
}: {
  env: string
  teammate: Teammate
  computers: Sandbox[]
  terminals: AgentTerminal[]
  browsing: boolean
  browserWaiting: boolean
  watching: boolean
  onWatch: () => void
  onOpenComputer: (c: Sandbox, tab?: ScreenTab, term?: string) => void
}) {
  const rows = terminals.flatMap((t) => {
    const c = computers.find((x) => x.id === t.sandboxId)
    if (!c) return []
    return [
      <NowRow
        key={`${t.sandboxId}-${t.name}`}
        lead={<ThreadDot status="terminal" />}
        title={t.title || `${teammate.name} in a terminal`}
        meta={`${t.state === "working" ? "working" : "in the terminal"} on ${c.name}`}
        onOpen={() => onOpenComputer(c, "terminal", t.name)}
      />,
    ]
  })
  if (rows.length === 0 && !browsing) return null
  return (
    <section
      aria-label="Now"
      className="grid gap-2 rounded-xl border border-l-3 border-l-(--st-running) bg-card px-3.5 py-3 shadow-xs"
    >
      {rows}
      {browsing && (
        <BrowsingCard
          env={env}
          teammate={teammate.id}
          name={teammate.name}
          waiting={browserWaiting}
          watching={watching}
          onWatch={onWatch}
          className="ml-0"
        />
      )}
    </section>
  )
}

/** One thing that runs, on the Now card: its thread, or its agent in a terminal. */
function NowRow({
  lead,
  title,
  meta,
  onOpen,
}: {
  lead: ReactNode
  title: string
  meta: string
  onOpen: () => void
}) {
  return (
    <div className="flex min-w-0 items-center gap-2.5">
      <span className="flex w-3.5 shrink-0 justify-center">{lead}</span>
      <span className="min-w-0 flex-1 truncate text-sm">{title}</span>
      <span className="max-w-[40%] shrink-0 truncate text-xs text-muted-foreground">
        {meta}
      </span>
      <Button
        size="xs"
        variant="outline"
        aria-label={`Open ${title}`}
        onClick={onOpen}
      >
        Open
      </Button>
    </div>
  )
}

/**
 * The teammate's computers, as cards with what happens on each and one click into its
 * terminal, browser or files; or, while it has none, the offer to give it one. On a narrow page
 * the first two show, then "All N computers".
 */
function ComputerCards({
  env,
  teammate,
  computers,
  loaded,
  onOpen,
}: {
  env: string
  teammate: Teammate
  computers: Sandbox[]
  loaded: boolean
  onOpen: (c: Sandbox, tab?: ScreenTab) => void
}) {
  const [giving, setGiving] = useState(false)
  const [all, setAll] = useState(false)
  const give = (
    <CreateSandboxDialog
      env={env}
      teammate={teammate.id}
      open={giving}
      onOpenChange={setGiving}
    />
  )
  if (!loaded) return null
  if (computers.length === 0)
    return (
      <section
        aria-label={`${teammate.name}'s computer`}
        className="grid justify-items-start gap-2 rounded-xl border border-dashed px-3.5 py-3"
      >
        <p className="text-sm font-medium">
          {teammate.name} has no computer yet
        </p>
        <p className="text-xs/relaxed text-muted-foreground">
          Give it one to run code, use its browser and show you previews.
        </p>
        <Button size="sm" variant="outline" onClick={() => setGiving(true)}>
          <PlusIcon />
          Give {teammate.name} a computer
        </Button>
        {give}
      </section>
    )
  const byName = new Map(computers.map((c) => [c.id, c.name]))
  return (
    <div className="grid gap-2">
      <span className="section-label text-muted-foreground">Computers</span>
      {computers.map((c, i) => (
        <ComputerCard
          key={c.id}
          env={env}
          computer={c}
          forkOf={c.forkOf ? byName.get(c.forkOf) : undefined}
          className={cn(i >= foldedCards && !all && "hidden @4xl:grid")}
          onOpen={(tab) => onOpen(c, tab)}
        />
      ))}
      {computers.length > foldedCards && !all && (
        <Button
          size="xs"
          variant="ghost"
          className="justify-self-start text-muted-foreground @4xl:hidden"
          onClick={() => setAll(true)}
        >
          All {computers.length} computers
        </Button>
      )}
      <button
        type="button"
        aria-label={`Give ${teammate.name} another computer`}
        onClick={() => setGiving(true)}
        className="flex items-center gap-1.5 justify-self-start rounded-sm px-1 text-xs text-muted-foreground outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring/40"
      >
        <PlusIcon className="size-3.5" />
        Another computer
      </button>
      {give}
    </div>
  )
}

/**
 * One of the teammate's computers: its state, and one click into its terminal, browser or
 * files. What runs on it is its threads' to say, on their cards. One that is off leads with Start (Resume when suspended), and its
 * Terminal and Files start it too, then open on the glass while it boots. Browser steps aside
 * until it runs, to keep the card to three buttons; the screen's own tab still opens it.
 */
function ComputerCard({
  env,
  computer: c,
  forkOf,
  className,
  onOpen,
}: {
  env: string
  computer: Sandbox
  /** The name of the computer it is a fork of, when that one is the teammate's too. */
  forkOf?: string
  className?: string
  onOpen: (tab?: ScreenTab) => void
}) {
  const { path, start, resume, phase: p } = useSandboxLifecycle(env, c)
  const action = wakeAction(c)
  // One that can't start by itself (a lost VM, disks in the old layout) opens on the glass,
  // which says why and offers the way out.
  const startable = !!action && !c.oldDiskLayout && c.status !== "absent"
  const mutation = action === "resume" ? resume : start
  const starting = p === "booting" || mutation.isPending
  const wake = () => {
    if (startable && !mutation.isPending) mutation.mutate(path)
  }
  const tabs = (
    [
      ["terminal", TerminalWindowIcon, "Terminal"],
      ["browser", BrowserIcon, "Browser"],
      ["files", FolderIcon, "Files"],
    ] as const
  ).filter(([tab]) => tab !== "browser" || !(startable || starting))
  return (
    <div
      data-testid="computer-card"
      className={cn(
        "grid min-w-0 gap-2 rounded-xl border bg-card px-3 py-2.5 shadow-xs",
        className
      )}
    >
      <span className="flex min-w-0 items-center gap-2">
        <StatusDot
          {...(starting ? phaseDot("booting") : phaseDot(p))}
          compact
        />
        <button
          type="button"
          title={`${c.name}: its details`}
          className="min-w-0 truncate rounded-sm font-mono text-sm font-medium underline-offset-2 outline-none hover:underline focus-visible:ring-2 focus-visible:ring-ring"
          onClick={() => onOpen("details")}
        >
          {c.name}
        </button>
        {c.forkDefaults && <BaseMark />}
        <span className="ml-auto shrink-0 text-caption text-muted-foreground">
          {starting ? "Starting…" : phaseDot(p).label}
        </span>
      </span>
      {(starting || forkOf || startable) && (
        <span className="min-w-0 truncate text-xs text-muted-foreground">
          {starting ? (
            "Its terminals open once it is up"
          ) : forkOf ? (
            <>
              A fork of <span className="font-mono">{forkOf}</span>
            </>
          ) : (
            "Its workspace, home and Docker images are kept"
          )}
        </span>
      )}
      <span className="flex flex-wrap gap-1">
        {(startable || starting) && (
          <Button
            size="xs"
            aria-label={
              starting
                ? `${c.name} is starting`
                : `${action === "resume" ? "Resume" : "Start"} ${c.name}`
            }
            disabled={starting}
            className="border-transparent bg-(--st-running) text-(--st-running-on) hover:bg-(--st-running)/90 disabled:opacity-70"
            onClick={wake}
          >
            {starting ? <Spinner /> : <PowerIcon />}
            {starting ? "Starting" : action === "resume" ? "Resume" : "Start"}
          </Button>
        )}
        {tabs.map(([tab, I, label]) => (
          <Button
            key={tab}
            size="xs"
            variant="outline"
            aria-label={`${label} of ${c.name}`}
            onClick={() => {
              wake()
              onOpen(tab)
            }}
          >
            <I />
            {label}
          </Button>
        ))}
      </span>
    </div>
  )
}

/** How many computer cards a narrow home shows before "All N computers". */
const foldedCards = 2

/** How many threads the desk lists before "All threads". */
const deskThreads = 8

/**
 * The home on the desk, narrowed to a panel beside the open computer: what waits, the threads
 * and the computers in brief, and the composer. Expand gives it the page again; Fold leaves the
 * computer the page.
 */
function DeskHome({
  env,
  teammate,
  chats,
  computers,
  computer,
  waiting,
  watching,
  onThread,
  onReview,
  onOpenComputer,
  onExpand,
}: HomeProps) {
  const pending = usePendingApprovals()
  const asks = (pending.data ?? []).filter((a) => a.environmentId === env)
  const shownWaiting = watching
    ? waiting.filter((a) => !isBrowserAsk(a, teammate.id))
    : waiting
  const inTerminals = useAgentTerminals(env, computers)
  const sorted = [...chats].sort((a, b) =>
    b.updatedAt.localeCompare(a.updatedAt)
  )
  const status = (c: Chat) => chatStatus(c, chatApprovals(c, asks).length)
  const live = sorted.filter(
    (c) => status(c) !== "done" && status(c) !== "ended"
  )
  const listed = [
    ...live,
    ...sorted
      .filter((c) => !live.includes(c))
      .slice(0, Math.max(0, deskThreads - live.length)),
  ]
  const computerName = (id: string) =>
    computers.length > 1 ? computers.find((c) => c.id === id)?.name : undefined
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <header className="flex h-12 shrink-0 items-center gap-1 pr-1.5 pl-3">
        <h2 className="mr-1 font-heading text-sm font-semibold">Home</h2>
      </header>
      <div className="grid min-h-0 flex-1 content-start gap-4 overflow-y-auto px-3 pb-3">
        {shownWaiting.length > 0 && (
          <Waiting approvals={shownWaiting} onReview={onReview} />
        )}
        <section aria-label="Threads" className="grid gap-1">
          <span className="px-1 section-label text-muted-foreground">
            Threads
          </span>
          {inTerminals.map((t) => {
            const c = computers.find((x) => x.id === t.sandboxId)
            return (
              c && (
                <DeskRow
                  key={`${t.sandboxId}-${t.name}`}
                  status="terminal"
                  title={t.title || `${teammate.name} in a terminal`}
                  meta={t.state === "working" ? "working" : "in the terminal"}
                  where={computerName(c.id)}
                  onClick={() => onOpenComputer(c, "terminal", t.name)}
                />
              )
            )
          })}
          {listed.map((c) => (
            <ChatContextMenu
              key={c.id}
              env={env}
              chat={c}
              onTerminal={(term) => {
                const sb = computers.find((x) => x.id === c.sandboxId)
                if (sb) onOpenComputer(sb, "terminal", term)
              }}
            >
              <DeskRow
                status={status(c)}
                title={chatTitle(c)}
                meta={statusLabels[status(c)]}
                where={computerName(c.sandboxId)}
                onClick={() => onThread(c)}
              />
            </ChatContextMenu>
          ))}
          {chats.length === 0 && inTerminals.length === 0 && (
            <p className="px-1 text-xs text-muted-foreground">
              No threads yet. Ask {teammate.name} something below.
            </p>
          )}
          {chats.length > listed.length && (
            <button
              type="button"
              onClick={onExpand}
              className="justify-self-start px-1 text-xs text-muted-foreground underline-offset-4 hover:text-foreground hover:underline"
            >
              All {chats.length} threads
            </button>
          )}
        </section>
        <section aria-label="Computers" className="grid gap-px">
          <span className="mb-1 px-1 section-label text-muted-foreground">
            Computers
          </span>
          {computers.map((c) => {
            const shown = c.id === computer?.id
            return (
              <button
                key={c.id}
                type="button"
                aria-current={shown || undefined}
                onClick={() => onOpenComputer(c)}
                className={cn(
                  "flex min-w-0 items-center gap-2 rounded-md px-2 py-1.5 text-left font-mono text-xs outline-none hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring/40",
                  shown && "bg-muted font-semibold"
                )}
              >
                <StatusDot {...phaseDot(phase(c))} compact />
                <span className="min-w-0 flex-1 truncate">{c.name}</span>
                {c.forkDefaults && <BaseMark />}
                {shown && (
                  <span className="font-sans text-caption font-normal text-muted-foreground">
                    shown
                  </span>
                )}
              </button>
            )
          })}
        </section>
      </div>
      <NewThread
        env={env}
        teammate={teammate}
        computer={computer}
        computers={computers}
        onThread={onThread}
      />
    </div>
  )
}

/** A row of the desk; the rest of its props go to its button, such as a context menu's trigger. */
function DeskRow({
  status,
  title,
  meta,
  where,
  onClick,
  ...props
}: {
  status: ThreadStatus
  title: string
  meta: string
  where?: string
  onClick: () => void
} & Omit<ComponentProps<"button">, "title" | "onClick">) {
  return (
    <button
      type="button"
      {...props}
      onClick={onClick}
      className="flex min-w-0 items-center gap-2.5 rounded-lg border bg-card px-2.5 py-2 text-left text-sm outline-none hover:border-foreground/20 focus-visible:ring-2 focus-visible:ring-ring/40"
    >
      <span className="flex w-3.5 shrink-0 justify-center">
        <ThreadDot status={status} />
      </span>
      <span className="min-w-0 flex-1 truncate">{title}</span>
      {where && (
        <span className="shrink-0 font-mono text-caption text-muted-foreground max-lg:hidden">
          {where}
        </span>
      )}
      <span
        className={cn(
          "shrink-0 text-caption text-muted-foreground",
          status === "waiting" && "text-(--st-waiting-fg)"
        )}
      >
        {meta}
      </span>
    </button>
  )
}

function DayDivider({ label }: { label: string }) {
  return (
    <div className="flex items-center gap-3 pt-3 pb-0.5 section-label text-muted-foreground">
      <span className="h-px flex-1 bg-border" />
      {label}
      <span className="h-px flex-1 bg-border" />
    </div>
  )
}

/** Markdown down to one plain paragraph, its `code` kept: a card's preview of an answer. */
function Preview({ text }: { text: string }) {
  const para =
    text
      .replace(/```[\s\S]*?(```|$)/g, " ")
      .split(/\n\s*\n/)
      .map((p) => p.trim())
      .find((p) => p && !/^[-*_]{3,}$/.test(p)) ?? ""
  const plain = para
    .replace(/^#+\s*/gm, "")
    .replace(/^\s*[-*+]\s+/gm, "")
    .replace(/\[([^\]]+)\]\([^)]*\)/g, "$1")
    .replace(/(\*\*|__)(.+?)\1/g, "$2")
    .replace(/\s+/g, " ")
  return (
    <>
      {plain.split("`").map((part, i) =>
        i % 2 === 1 ? (
          <code
            key={i}
            className="rounded-sm border bg-muted px-1 font-mono text-xs"
          >
            {part}
          </code>
        ) : (
          part
        )
      )}
    </>
  )
}

/** One thread in brief: its title, where it runs, the last answer and how it stands. */
function ThreadCard({
  chat,
  asks,
  turns,
  computer,
  live,
  news,
  onOpen,
}: {
  chat: Chat
  /** What waits for you in it. */
  asks: Approval[]
  /** Its requests in the last two weeks, oldest first. */
  turns: Turn[]
  /** The computer it runs on, named when the teammate has more than one. */
  computer?: string
  live?: ReturnType<typeof useChat>
  /** It changed since this browser last looked. */
  news: boolean
  onOpen: () => void
}) {
  const last = turns.at(-1)
  const tool = live
    ? [...live.items].reverse().find((i) => i.kind === "tool")?.tool?.title
    : undefined
  const said =
    (live
      ? [...live.items]
          .reverse()
          .find((i) => i.kind === "agent" && i.text?.text)?.text?.text
      : undefined) ??
    last?.answer ??
    last?.prompt
  const kind = chat.heartbeat
    ? { icon: ClockIcon, label: "Check-in" }
    : chat.taskId
      ? { icon: ClockIcon, label: "Scheduled" }
      : undefined
  const K = kind?.icon ?? ChatCircleIcon
  const marker = last?.marker
    ? (last.markerText ??
      {
        stopped: "Stopped",
        interrupted: "Interrupted",
        error: "Something went wrong",
        limit: "Hit a limit",
      }[last.marker])
    : undefined

  return (
    <button
      type="button"
      onClick={onOpen}
      className="@container/card grid w-full gap-1.5 rounded-xl border bg-card px-3.5 py-3 text-left shadow-xs transition-[border-color,box-shadow] outline-none hover:border-foreground/20 hover:shadow-sm focus-visible:ring-2 focus-visible:ring-ring/40"
    >
      <span className="flex min-w-0 items-center gap-2">
        <K
          className={cn(
            "size-4 shrink-0 text-muted-foreground",
            news && "text-primary"
          )}
          weight={news ? "fill" : "regular"}
        />
        {kind && (
          <span className="shrink-0 text-xs text-muted-foreground">
            {kind.label} ·
          </span>
        )}
        <span className="min-w-0 truncate text-sm font-medium">
          {chatTitle(chat)}
        </span>
        {computer && (
          <span className="shrink-0 rounded-full border px-2 font-mono text-caption text-muted-foreground @max-[26rem]/card:hidden">
            on {computer}
          </span>
        )}
        {news && (
          <span
            title="New since you last looked"
            className="ml-auto shrink-0 rounded-full bg-primary/10 px-1.5 text-meta leading-4 font-medium text-primary"
          >
            New
          </span>
        )}
        <span
          className={cn(
            "shrink-0 text-xs text-muted-foreground",
            news ? "pl-0.5" : "ml-auto pl-2"
          )}
        >
          {ago(chat.updatedAt)}
        </span>
      </span>
      {said && (
        <span className="line-clamp-2 text-xs/relaxed">
          <Preview text={said} />
        </span>
      )}
      <span className="flex min-w-0 items-center gap-1.5 text-caption text-muted-foreground">
        {asks.length > 0 ? (
          <>
            <span className="size-1.5 shrink-0 rounded-full bg-(--st-waiting)" />
            <span className="min-w-0 truncate font-medium text-(--st-waiting-fg)">
              {askWords(asks[0])}
              {asks.length > 1 && ` · ${asks.length - 1} more`}
            </span>
          </>
        ) : (
          <ThreadState chat={chat} turns={turns} tool={tool} marker={marker} />
        )}

        {/* A narrow card leaves the title line to the title. */}
        {computer && (
          <span className="hidden min-w-0 truncate @max-[26rem]/card:inline">
            · on <span className="font-mono">{computer}</span>
          </span>
        )}
      </span>
    </button>
  )
}

const askVerbs: Record<string, string> = {
  execute: "run",
  edit: "edit",
  delete: "delete",
  move: "move",
  fetch: "fetch",
  read: "read",
  search: "search",
}

/** What a thread waits for, in words: "Asks to run make test", or "Git push: fix → team/app". */
export function askWords(a: Approval) {
  const p = a.chatPermission
  if (!p) {
    const k = approvalKind(a)
    return `${k.title}: ${k.summary(a)}`
  }
  const verb = askVerbs[p.toolKind ?? ""]
  const t = p.title.trim()
  if (verb && t.toLowerCase().startsWith(`${verb} `))
    return `Asks to ${verb}${t.slice(verb.length)}`
  return verb ? `Asks to ${verb} ${t}` : `Asks before going on: ${t}`
}

/** A thread card's foot while nothing waits: its requests, and whether it works or how it ended. */
function ThreadState({
  chat,
  turns,
  tool,
  marker,
}: {
  chat: Chat
  turns: Turn[]
  tool?: string
  marker?: string
}) {
  const running = chat.state === "running"
  return (
    <>
      {turns.length > 0 && (
        <>
          <span className="shrink-0">
            {turns.length} {turns.length === 1 ? "request" : "requests"}
          </span>
          <span aria-hidden>·</span>
        </>
      )}
      {running ? (
        <>
          <span className="size-1.5 shrink-0 rounded-full bg-(--st-running)" />
          <span className="min-w-0 shimmer truncate">
            {tool ? `${tool}…` : "Working"}
          </span>
        </>
      ) : marker ? (
        <span className="text-destructive">{marker}</span>
      ) : chat.state === "terminal" ? (
        <span>In the terminal</span>
      ) : chat.state === "orphaned" ? (
        <span>Ended</span>
      ) : (
        <span>Done</span>
      )}
    </>
  )
}

/** What happened between threads: a fact learned, a task added or done, terminal work. */
function SmallEvent({
  env,
  event: e,
  onTasks,
}: {
  env: string
  event: Event
  onTasks: () => void
}) {
  if (e.fact) return <Learned env={env} at={e.at} fact={e.fact} />
  if (e.task) {
    const done = e.task.event === "done"
    return (
      <Row icon={done ? CheckCircleIcon : ListChecksIcon} at={e.at}>
        <button
          type="button"
          onClick={onTasks}
          className="text-left hover:text-foreground"
        >
          {done
            ? "Finished a task: "
            : e.task.createdBy === "teammate"
              ? "Added a task for itself: "
              : "Task added: "}
          <span className="font-medium text-foreground">{e.task.title}</span>
        </button>
      </Row>
    )
  }
  if (e.session)
    return (
      <Row icon={TerminalWindowIcon} at={e.at}>
        {e.session.summary}
      </Row>
    )
  return null
}

function Row({
  icon: I,
  at,
  className,
  iconClassName,
  children,
  action,
}: {
  icon: Icon
  at: string
  className?: string
  iconClassName?: string
  children: ReactNode
  action?: ReactNode
}) {
  return (
    <div
      className={cn(
        "group/row flex min-w-0 items-start gap-2.5 px-3.5 py-1 text-xs/relaxed text-muted-foreground",
        className
      )}
    >
      <I className={cn("mt-[3px] size-3.5 shrink-0", iconClassName)} />
      <span className="min-w-0 flex-1 wrap-anywhere">{children}</span>
      {action}
      <span className="shrink-0 pt-px font-mono text-meta">{time(at)}</span>
    </div>
  )
}

/** A fact the teammate learned, from you or from its own work; forget it in place. */
function Learned({
  env,
  at,
  fact,
}: {
  env: string
  at: string
  fact: NonNullable<Event["fact"]>
}) {
  const queryClient = useQueryClient()
  const [busy, setBusy] = useState(false)
  const forgotten = fact.status === "retracted"
  const forget = async () => {
    setBusy(true)
    const { error } = await fetchClient.POST(
      "/api/environments/{env}/memory/facts/{id}/retract",
      { params: { path: { env, id: fact.id } } }
    )
    setBusy(false)
    if (error) {
      toast.error(errorMessage(error))
      return
    }
    for (const path of [
      "/api/environments/{env}/teammates/{id}/timeline",
      "/api/environments/{env}/memory/facts",
    ])
      void queryClient.invalidateQueries({ queryKey: ["get", path] })
  }
  return (
    <Row
      icon={SparkleIcon}
      at={at}
      iconClassName="text-(--acc-memory)"
      className={cn(forgotten && "opacity-60")}
      action={
        !forgotten && (
          <Button
            size="xs"
            variant="ghost"
            className="-my-0.5 h-5 shrink-0 px-1.5 text-caption text-muted-foreground opacity-0 group-hover/row:opacity-100 focus-visible:opacity-100"
            disabled={busy}
            onClick={() => void forget()}
          >
            Forget
          </Button>
        )
      }
    >
      <span className={cn(forgotten && "line-through")}>
        {forgotten
          ? "Forgot that "
          : fact.tier === "user"
            ? "Learned from you that "
            : "Learned that "}
        {fact.text.charAt(0).toLowerCase() + fact.text.slice(1)}
      </span>
    </Row>
  )
}

/**
 * What waits for you on this teammate, as the Needs you rows of every page
 * (components/needs-you.tsx): a push opens on the screen's Changes, a memory conflict in a sheet,
 * and anything else answers in its row or opens there with all its choices.
 */
function Waiting({
  approvals,
  onReview,
}: {
  approvals: Approval[]
  onReview: (a: Approval) => void
}) {
  useClaimApprovals(approvals.map((a) => a.id))
  const { sheet, review } = useApprovalReview()
  return (
    <section aria-label="Needs you" className="grid gap-2">
      <h2 className="px-1 section-label text-muted-foreground">
        Needs you · {approvals.length}
      </h2>
      <NeedsYouList
        approvals={approvals}
        tips
        onReview={(a) => (isGitApproval(a) ? onReview(a) : review(a))}
      />
      {sheet}
    </section>
  )
}

/**
 * The home's composer: a message starts a new thread on the computer on the screen; a task is
 * added to the list and a thread starts on it; teaching writes a fact to memory, with no thread.
 */
function NewThread({
  env,
  teammate,
  computer: shown,
  computers,
  onThread,
}: {
  env: string
  teammate: Teammate
  /** The computer on the screen, where a new thread starts unless the user picks another. */
  computer?: Sandbox
  computers: Sandbox[]
  onThread: (chat: Chat) => void
}) {
  const box = useRef<HTMLDivElement>(null)
  useNewThreadFocus(teammate.id, box)
  const queryClient = useQueryClient()
  const [picked, setPicked] = useState<string>()
  const computer = computers.find((c) => c.id === picked) ?? shown
  // Typing a message or a task to a stopped computer starts it, and the thread waits for it.
  const wake = useWakeOnType(env, computer)
  const [mode, setMode] = useState<Mode>("message")
  const [problem, setProblem] = useState<string>()
  // The model a new thread starts on: "" for the teammate's default.
  const [model, setModel] = useState("")

  const refresh = () => {
    for (const path of [
      "/api/environments/{env}/teammates/{id}/timeline",
      "/api/environments/{env}/chats",
      "/api/environments/{env}/teammates/{id}/tasks",
      "/api/environments/{env}/memory/facts",
    ])
      void queryClient.invalidateQueries({ queryKey: ["get", path] })
  }

  // The thread opens at once and sends the message itself, so that it shows there while the
  // computer and the agent start.
  const start = async (body: Prompt) => {
    if (!computer)
      throw new Error(
        `${teammate.name} has no computer yet: give it one on the right.`
      )
    const { data, error } = await fetchClient.POST(
      "/api/environments/{env}/sandboxes/{id}/chats",
      {
        params: { path: { env, id: computer.id } },
        body: { model: teammate.models?.includes(model) ? model : undefined },
      }
    )
    if (error) throw error
    handFirstMessage(data.id, { prompt: body, ready: wake.ready() })
    return data
  }

  const send = async (p: Prompt) => {
    setProblem(undefined)
    const text = p.text?.trim() ?? ""
    try {
      if (mode === "teach") {
        const { error } = await fetchClient.POST(
          "/api/environments/{env}/memory/facts",
          {
            params: { path: { env } },
            body: { scope: `teammate:${teammate.id}`, kind: "fact", text },
          }
        )
        if (error) throw error
        toast.success(`${teammate.name} will remember that`)
        refresh()
        return true
      }
      if (mode === "task") {
        const [first, ...rest] = text.split("\n")
        const title = first.slice(0, 200)
        const details = rest.join("\n").trim()
        const { error } = await fetchClient.POST(
          "/api/environments/{env}/teammates/{id}/tasks",
          {
            params: { path: { env, id: teammate.id } },
            body: { title, details: details || undefined },
          }
        )
        if (error) throw error
        const chat = await start({
          text: `I added a task to your list: "${title}".${details ? `\n\n${details}` : ""}\n\nPlease work on it now: mark it in progress, and done when you finish.`,
        })
        refresh()
        onThread(chat)
        return true
      }
      const chat = await start(p)
      refresh()
      onThread(chat)
      return true
    } catch (err) {
      setProblem(errorMessage(err))
      return false
    }
  }

  const M = modes[mode]
  const placeholder = {
    message: `Message ${teammate.name}…`,
    task: `Hand ${teammate.name} a task: the first line is its title`,
    teach: `Tell ${teammate.name} something to remember for good…`,
  }[mode]
  const where =
    mode === "teach"
      ? `Saved to ${teammate.name}'s memory`
      : computer
        ? mode === "task"
          ? "Added to the tasks, then a new thread on"
          : "Starts a new thread on"
        : `${teammate.name} needs a computer first`
  // The computer is off and typing starts it: the line says so before it happens.
  const off =
    mode !== "teach" &&
    !!computer &&
    (wake.waking ||
      (!!wakeAction(computer) &&
        !computer.oldDiskLayout &&
        computer.status !== "absent"))
  const pick = mode !== "teach" && computer && (
    <DropdownMenu modal={false}>
      <DropdownMenuTrigger
        aria-label={`Computer: ${computer.name}`}
        disabled={computers.length < 2}
        className="inline-flex h-6 min-w-0 shrink items-center gap-1 rounded-md px-1 font-mono text-foreground enabled:hover:bg-muted data-popup-open:bg-muted"
      >
        <span className="truncate">{computer.name}</span>
        {computers.length > 1 && (
          <CaretDownIcon className="size-2.5 shrink-0 text-muted-foreground" />
        )}
      </DropdownMenuTrigger>
      <DropdownMenuContent side="top" align="start" className="w-56">
        {computers.map((c) => (
          <DropdownMenuItem key={c.id} onClick={() => setPicked(c.id)}>
            <StatusDot {...phaseDot(phase(c))} compact />
            <span className="min-w-0 flex-1 truncate font-mono">{c.name}</span>
            {c.id === shown?.id && (
              <span className="text-caption text-muted-foreground">shown</span>
            )}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  )

  return (
    <ComposerDock ref={box} className="@container/composer max-w-5xl">
      <Composer
        onSend={send}
        error={problem}
        placeholder={placeholder}
        draft={`teammate:${teammate.id}`}
        onInput={mode === "teach" ? undefined : wake.wake}
        attachments={mode === "message"}
        mentionsIn={
          mode === "message" && computer
            ? { env, sandbox: computer.id }
            : undefined
        }
        className={cn(
          "rounded-xl",
          mode === "teach" && "border-(--acc-memory)/60",
          mode === "task" && "border-(--acc-chat)/60"
        )}
        hint={
          <span className="flex min-w-0 items-center gap-1.5">
            <DropdownMenu modal={false}>
              <DropdownMenuTrigger
                aria-label={`Mode: ${M.label}`}
                className="inline-flex h-6 shrink-0 items-center gap-1 rounded-md px-1.5 text-foreground hover:bg-muted data-popup-open:bg-muted"
              >
                <M.icon className="size-3.5" />
                {M.label}
                <CaretDownIcon className="size-2.5 text-muted-foreground" />
              </DropdownMenuTrigger>
              <DropdownMenuContent side="top" align="start" className="w-64">
                {(Object.keys(modes) as Mode[]).map((m) => {
                  const I = modes[m].icon
                  return (
                    <DropdownMenuItem key={m} onClick={() => setMode(m)}>
                      <I />
                      <span className="grid">
                        {modes[m].label}
                        <span className="text-caption text-muted-foreground">
                          {
                            {
                              message: "A new thread with your message",
                              task: "Added to its tasks; it starts right away",
                              teach: "A fact for its memory, no thread",
                            }[m]
                          }
                        </span>
                      </span>
                    </DropdownMenuItem>
                  )
                })}
              </DropdownMenuContent>
            </DropdownMenu>
            {mode !== "teach" && (
              <ModelPicker
                teammate={teammate}
                value={model}
                onChange={setModel}
              />
            )}
            {/* A narrow composer, as on the desk, says just "on" before the computer. */}
            <span
              className={cn("truncate", pick && "@max-[30rem]/composer:hidden")}
            >
              {where}
            </span>
            {pick && (
              <span className="hidden shrink-0 @max-[30rem]/composer:inline">
                on
              </span>
            )}
            {pick}
            {off && (
              <span className="shrink-0 truncate text-muted-foreground @max-[30rem]/composer:hidden">
                {wake.waking ? "· starting" : "· off, it starts as you type"}
              </span>
            )}
          </span>
        }
      />
    </ComposerDock>
  )
}
