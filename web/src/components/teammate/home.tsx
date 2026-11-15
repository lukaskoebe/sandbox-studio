import { useState, type ReactNode } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  ArrowLineLeftIcon,
  ArrowsOutLineHorizontalIcon,
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
  SparkleIcon,
  TerminalWindowIcon,
  UserPlusIcon,
  type Icon,
} from "@phosphor-icons/react"
import { BaseMark } from "@/components/base-dialog"
import { CreateSandboxDialog } from "@/components/create-sandbox-dialog"
import { phase, phaseDot } from "@/components/status-badge"
import { StatusDot } from "@/components/status-dot"
import {
  ApprovalCardList,
  useApprovalReview,
} from "@/components/approval-stack"
import { BrowsingCard, useBrowsing } from "@/components/browser-browsing"
import { isGitApproval } from "@/components/git-review"
import { ComposerDock } from "@/components/chat/chat-thread"
import { Composer, type Prompt } from "@/components/chat/composer"
import { ModelPicker } from "@/components/chat/model-picker"
import { TeammateHeader } from "@/components/teammate/header"
import { usePendingApprovals } from "@/hooks/use-pending-approvals"
import { TeammateAvatar } from "@/components/teammate-avatar"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import {
  MessageScroller,
  MessageScrollerButton,
  MessageScrollerContent,
  MessageScrollerItem,
  MessageScrollerProvider,
  MessageScrollerViewport,
} from "@/components/ui/message-scroller"
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
import {
  approvalCards,
  cardApproval,
  groupSite,
  type ApprovalCard,
} from "@/lib/approval-groups"
import {
  approvalKind,
  approvalSource,
  isBrowserAsk,
} from "@/lib/approval-kinds"
import { tileClass } from "@/lib/accents"
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
import { cn, formatAge } from "@/lib/utils"

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
  onProfile: (view: "card" | "tasks" | "memory" | "settings") => void
  onWatch: () => void
  /** Opens a push waiting for review on the screen's Changes. */
  onReview: (a: Approval) => void
  onStartWork: () => void
  /** Brings the teammate into what the user does in the terminal on the desk. */
  onBringIn?: () => void
  /** Opens one of its computers on the screen, on a tab and terminal session. */
  onOpenComputer: (c: Sandbox, tab?: ScreenTab, term?: string) => void
  /** The desk's home takes the page again, the computer folded to its rail. */
  onExpand: () => void
  /** The desk's home folds to a rail, the computer taking the page. */
  onFold: () => void
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
 * The home with the page to itself: its threads as cards by day, what it learned, its tasks
 * and terminal work between them, what waits for you at the end, and a composer that starts a
 * new thread; beside them its computers, each one click from its terminal, browser and files.
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
  onStartWork,
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
          teammate={teammate}
          chat={chat}
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
  ].sort((a, b) => a.at.localeCompare(b.at))

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
      chats={chats}
      loaded={loaded}
      onOpen={onOpenComputer}
    />
  )

  return (
    <div className="@container flex min-h-0 flex-1 flex-col">
      <TeammateHeader
        env={env}
        teammate={teammate}
        onProfile={onProfile}
        onStartWork={onStartWork}
      />
      <div className="shrink-0 px-4 pb-2 @4xl:hidden">{cards}</div>
      <div className="flex min-h-0 flex-1">
        <div className="flex min-h-0 min-w-0 flex-1 flex-col">
          <MessageScrollerProvider defaultScrollPosition="end" autoScroll>
            <MessageScroller className="min-h-0 flex-1">
              <MessageScrollerViewport
                aria-label={`Threads with ${teammate.name}`}
              >
                <MessageScrollerContent className="mx-auto w-full max-w-2xl gap-2.5 px-4 pt-2 pb-4">
                  {timeline.isPending && (
                    <MessageScrollerItem className="m-auto">
                      <Spinner />
                    </MessageScrollerItem>
                  )}
                  {timeline.error && (
                    <MessageScrollerItem>
                      <p role="alert" className="text-xs text-destructive">
                        {errorMessage(timeline.error)}
                      </p>
                    </MessageScrollerItem>
                  )}
                  {empty && (
                    <MessageScrollerItem className="m-auto grid max-w-xs justify-items-center gap-2 text-center">
                      <TeammateAvatar
                        name={teammate.name}
                        avatar={teammate.avatar}
                        className="size-11 text-base"
                      />
                      <p className="text-sm font-medium">
                        This is where you and {teammate.name} talk.
                      </p>
                      <p className="text-xs text-muted-foreground">
                        {computer
                          ? `Ask ${teammate.name} something and it starts a thread here.`
                          : `Give ${teammate.name} a computer, then ask it something: each request starts a thread here.`}
                      </p>
                    </MessageScrollerItem>
                  )}
                  {hidden > 0 && (
                    <MessageScrollerItem className="flex justify-center">
                      <Button
                        variant="ghost"
                        size="xs"
                        className="text-muted-foreground"
                        onClick={() => setOlder(true)}
                      >
                        {hidden} earlier {hidden === 1 ? "thread" : "threads"}
                      </Button>
                    </MessageScrollerItem>
                  )}
                  {rows.map((row, i) => (
                    <MessageScrollerItem key={i}>{row}</MessageScrollerItem>
                  ))}
                  {inTerminals.map((t) => (
                    <MessageScrollerItem key={`${t.sandboxId}-${t.name}`}>
                      <TerminalCard
                        teammate={teammate}
                        terminal={t}
                        computer={computers.find((c) => c.id === t.sandboxId)}
                        onOpen={(c) => onOpenComputer(c, "terminal", t.name)}
                      />
                    </MessageScrollerItem>
                  ))}
                  {browsing && (
                    <MessageScrollerItem>
                      <BrowsingCard
                        env={env}
                        teammate={id}
                        name={teammate.name}
                        waiting={browserWaiting}
                        watching={watching}
                        onWatch={onWatch}
                      />
                    </MessageScrollerItem>
                  )}
                  {shownWaiting.length > 0 && (
                    <MessageScrollerItem>
                      <Waiting
                        env={env}
                        approvals={shownWaiting}
                        onReview={onReview}
                      />
                    </MessageScrollerItem>
                  )}
                </MessageScrollerContent>
              </MessageScrollerViewport>
              <MessageScrollerButton />
            </MessageScroller>
          </MessageScrollerProvider>
          <NewThread
            env={env}
            teammate={teammate}
            computer={computer}
            computers={computers}
            onThread={onThread}
          />
        </div>
        <aside
          aria-label={`${teammate.name}'s computers`}
          className="hidden w-72 shrink-0 overflow-y-auto border-l px-3 py-1 @4xl:block"
        >
          {cards}
        </aside>
      </div>
    </div>
  )
}

/** A thread's state as a dot, the way the sidebar and the desk list threads. */
export function ThreadDot({
  status,
  className,
}: {
  status: ThreadStatus
  className?: string
}) {
  if (status === "terminal")
    return (
      <span
        aria-hidden
        className={cn(
          "shrink-0 font-mono text-meta leading-none font-semibold text-muted-foreground",
          className
        )}
      >
        &gt;_
      </span>
    )
  return (
    <span
      aria-hidden
      className={cn(
        "size-2 shrink-0 rounded-full",
        status === "working" && "animate-pulse bg-(--st-running)",
        status === "waiting" && "bg-(--st-waiting)",
        (status === "done" || status === "ended") &&
          "ring-1 ring-muted-foreground/60 ring-inset",
        className
      )}
    />
  )
}

/**
 * The teammate's computers, as cards with what happens on each and one click into its
 * terminal, browser or files; or, while it has none, the offer to give it one.
 */
function ComputerCards({
  env,
  teammate,
  computers,
  chats,
  loaded,
  onOpen,
}: {
  env: string
  teammate: Teammate
  computers: Sandbox[]
  chats: Chat[]
  loaded: boolean
  onOpen: (c: Sandbox, tab?: ScreenTab) => void
}) {
  const [giving, setGiving] = useState(false)
  const pending = usePendingApprovals()
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
        className="my-2 grid justify-items-start gap-2 rounded-xl border border-dashed px-3.5 py-3"
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
  const asks = (pending.data ?? []).filter((a) => a.environmentId === env)
  const byName = new Map(computers.map((c) => [c.id, c.name]))
  return (
    <div className="flex gap-2 overflow-x-auto py-2 @4xl:grid @4xl:overflow-visible">
      <span className="hidden section-label text-muted-foreground @4xl:block">
        {teammate.name}'s computers
      </span>
      {computers.map((c) => {
        const p = phase(c)
        const here = chats
          .filter((x) => x.sandboxId === c.id)
          .sort((a, b) => b.updatedAt.localeCompare(a.updatedAt))
        const waitingChat = here.find(
          (x) => chatApprovals(x, asks, chats).length > 0
        )
        const shown =
          here.find((x) => x.state === "running") ?? waitingChat ?? here.at(0)
        const status = shown
          ? chatStatus(shown, shown === waitingChat ? 1 : 0)
          : undefined
        return (
          <div
            key={c.id}
            data-testid="computer-card"
            className="grid w-60 shrink-0 gap-2 rounded-xl border bg-card px-3 py-2.5 shadow-xs @4xl:w-auto"
          >
            <span className="flex min-w-0 items-center gap-2">
              <StatusDot {...phaseDot(p)} compact />
              <span className="min-w-0 truncate font-mono text-sm font-medium">
                {c.name}
              </span>
              {c.forkDefaults && <BaseMark />}
              <span className="ml-auto shrink-0 text-caption text-muted-foreground">
                {phaseDot(p).label}
              </span>
            </span>
            <span className="flex min-w-0 items-center gap-1.5 text-xs text-muted-foreground">
              {shown && status ? (
                <>
                  <ThreadDot status={status} />
                  <span className="truncate">
                    {chatTitle(shown)}
                    {status === "working"
                      ? " is running here"
                      : status === "waiting"
                        ? " waits for you here"
                        : `, ${statusLabels[status]}`}
                  </span>
                </>
              ) : c.forkOf && byName.has(c.forkOf) ? (
                <span className="truncate">
                  A fork of{" "}
                  <span className="font-mono">{byName.get(c.forkOf)}</span>
                </span>
              ) : (
                <span>No thread here yet</span>
              )}
            </span>
            <span className="flex gap-1">
              {(
                [
                  ["terminal", TerminalWindowIcon, "Terminal"],
                  ["browser", BrowserIcon, "Browser"],
                  ["files", FolderIcon, "Files"],
                ] as const
              ).map(([tab, I, label]) => (
                <Button
                  key={tab}
                  size="xs"
                  variant="outline"
                  aria-label={`${label} of ${c.name}`}
                  onClick={() => onOpen(c, tab)}
                >
                  <I />
                  {label}
                </Button>
              ))}
            </span>
          </div>
        )
      })}
      <Button
        size="xs"
        variant="ghost"
        className="shrink-0 self-center justify-self-start text-muted-foreground"
        onClick={() => setGiving(true)}
      >
        <PlusIcon />
        Give {teammate.name} another computer
      </Button>
      {give}
    </div>
  )
}

/** The teammate's agent in a terminal with no chat, which opens on its computer's terminal. */
function TerminalCard({
  teammate,
  terminal: t,
  computer,
  onOpen,
}: {
  teammate: Teammate
  terminal: AgentTerminal
  computer?: Sandbox
  onOpen: (c: Sandbox) => void
}) {
  if (!computer) return null
  return (
    <button
      type="button"
      onClick={() => onOpen(computer)}
      className="@container/card flex w-full min-w-0 items-center gap-2.5 rounded-xl border bg-card px-3.5 py-2.5 text-left shadow-xs outline-none hover:border-foreground/20 focus-visible:ring-2 focus-visible:ring-ring/40"
    >
      <ThreadDot status="terminal" />
      <span className="min-w-0 flex-1 truncate text-sm font-medium">
        {t.title || `${teammate.name} in a terminal`}
      </span>
      <span className="max-w-[45%] shrink-0 truncate rounded-full border px-2 font-mono text-caption text-muted-foreground">
        on {computer.name}
      </span>
      {/* On a phone the title comes first; the icon says it is a terminal. */}
      <span className="shrink-0 text-xs text-muted-foreground @max-[26rem]/card:hidden">
        {t.state === "working" ? "working" : "in the terminal"}
      </span>
    </button>
  )
}

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
  onStartWork,
  onBringIn,
  onOpenComputer,
  onExpand,
  onFold,
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
  const status = (c: Chat) =>
    chatStatus(c, chatApprovals(c, asks, chats).length)
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
        <Button size="xs" variant="outline" onClick={onStartWork}>
          <PlusIcon />
          Start work
        </Button>
        {onBringIn && (
          <Button
            size="xs"
            variant="ghost"
            className="min-w-0 text-muted-foreground"
            title={`Start a thread with ${teammate.name} on what you are doing here`}
            onClick={onBringIn}
          >
            <UserPlusIcon />
            <span className="truncate">Bring {teammate.name} in</span>
          </Button>
        )}
        <span className="ml-auto flex shrink-0 items-center">
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label="Expand the home"
            title="Expand the home"
            className="text-muted-foreground hover:text-foreground"
            onClick={onExpand}
          >
            <ArrowsOutLineHorizontalIcon />
          </Button>
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label="Fold the home"
            title="Fold the home"
            className="text-muted-foreground hover:text-foreground"
            onClick={onFold}
          >
            <ArrowLineLeftIcon />
          </Button>
        </span>
      </header>
      <div className="grid min-h-0 flex-1 content-start gap-4 overflow-y-auto px-3 pb-3">
        {shownWaiting.length > 0 && (
          <Waiting env={env} approvals={shownWaiting} onReview={onReview} />
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
            <DeskRow
              key={c.id}
              status={status(c)}
              title={chatTitle(c)}
              meta={statusLabels[status(c)]}
              where={computerName(c.sandboxId)}
              onClick={() => onThread(c)}
            />
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

function DeskRow({
  status,
  title,
  meta,
  where,
  onClick,
}: {
  status: ThreadStatus
  title: string
  meta: string
  where?: string
  onClick: () => void
}) {
  return (
    <button
      type="button"
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
  teammate,
  chat,
  turns,
  computer,
  live,
  news,
  onOpen,
}: {
  teammate: Teammate
  chat: Chat
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
  const running = chat.state === "running"
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
        <span className="flex min-w-0 items-start gap-2">
          <TeammateAvatar
            name={teammate.name}
            avatar={teammate.avatar}
            className="mt-0.5 size-4 shrink-0 text-[0.5rem]"
          />
          <span className="line-clamp-2 text-xs/relaxed">
            <Preview text={said} />
          </span>
        </span>
      )}
      <span className="flex min-w-0 items-center gap-1.5 text-caption text-muted-foreground">
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
 * What waits for you on this teammate, one line each: what is asked, from where and when. A push
 * opens on the screen's Changes; anything else opens its card here, with all its choices.
 */
function Waiting({
  env,
  approvals,
  onReview,
}: {
  env: string
  approvals: Approval[]
  onReview: (a: Approval) => void
}) {
  useClaimApprovals(approvals.map((a) => a.id))
  const [split, setSplit] = useState<ReadonlySet<string>>(() => new Set())
  const [open, setOpen] = useState<ReadonlySet<string>>(() => new Set())
  const envs = $api.useQuery("get", "/api/environments")
  const { sheet, review } = useApprovalReview(approvals)
  const cards = approvalCards(approvals, split)
  const toggle = (key: string) =>
    setOpen((prev) => {
      const next = new Set(prev)
      if (!next.delete(key)) next.add(key)
      return next
    })
  return (
    <section aria-label="Waiting for you" className="grid gap-2 pt-3">
      <span className="flex items-center gap-2 section-label text-(--st-waiting)">
        <span className="size-1.5 rounded-full bg-(--st-waiting)" />
        Waiting for you
      </span>
      {cards.map((card) => {
        const a = cardApproval(card)
        if (!open.has(a.id))
          return (
            <WaitingRow
              key={a.id}
              card={card}
              onOpen={() =>
                isGitApproval(a)
                  ? onReview(a)
                  : a.kind === "memory.conflict"
                    ? review(a)
                    : toggle(a.id)
              }
            />
          )
        return (
          <div key={a.id} className="grid gap-1">
            <ApprovalCardList
              cards={[card]}
              max={1}
              envName={() => envs.data?.find((e) => e.id === env)?.name}
              onReview={review}
              onSplit={(ids) => setSplit((prev) => new Set([...prev, ...ids]))}
            />
            <button
              type="button"
              onClick={() => toggle(a.id)}
              className="justify-self-end px-1 text-xs text-muted-foreground underline-offset-4 hover:text-foreground hover:underline"
            >
              Show less
            </button>
          </div>
        )
      })}
      {sheet}
    </section>
  )
}

/** One thing waiting, folded to a line, with the button that answers or reviews it. */
function WaitingRow({
  card,
  onOpen,
}: {
  card: ApprovalCard
  onOpen: () => void
}) {
  const a = cardApproval(card)
  const kind = approvalKind(a)
  const group = card.kind === "group" ? card.approvals : undefined
  const site = group && groupSite(group)
  const title = group
    ? `${site ?? approvalSource(a) ?? "A sandbox"} wants ${group.length} hosts`
    : kind.summary(a)
  const details = [
    kind.title,
    approvalSource(a),
    formatAge(a.createdAt),
    a.attempts > 1 ? `${a.attempts} attempts` : undefined,
  ]
    .filter(Boolean)
    .join(" · ")
  return (
    <div
      data-testid="waiting-row"
      className="flex items-center gap-3 rounded-2xl border bg-card py-2.5 pr-2.5 pl-3"
    >
      <span
        className={cn(
          "grid size-7 shrink-0 place-items-center rounded-md",
          tileClass(kind.area)
        )}
      >
        <kind.icon weight="bold" className="size-3.5" aria-hidden />
      </span>
      <span className="grid min-w-0 flex-1 gap-0.5">
        <span
          title={title}
          className={cn(
            "truncate text-title font-medium",
            kind.mono && !group && "font-mono"
          )}
        >
          {title}
        </span>
        <span className="truncate text-xs text-muted-foreground">
          {details}
        </span>
      </span>
      <Button size="sm" variant="outline" onClick={onOpen}>
        {isGitApproval(a) || a.kind === "memory.conflict" ? "Review" : "Answer"}
      </Button>
    </div>
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
  const queryClient = useQueryClient()
  const [picked, setPicked] = useState<string>()
  const computer = computers.find((c) => c.id === picked) ?? shown
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

  const start = async (body: Prompt) => {
    if (!computer)
      throw new Error(
        `${teammate.name} has no computer yet: give it one on the right.`
      )
    const { data, error } = await fetchClient.POST(
      "/api/environments/{env}/sandboxes/{id}/chats",
      {
        params: { path: { env, id: computer.id } },
        body: {
          ...body,
          model: teammate.models?.includes(model) ? model : undefined,
        },
      }
    )
    if (error) throw error
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
    <ComposerDock className="@container/composer max-w-2xl">
      <Composer
        onSend={send}
        error={problem}
        placeholder={placeholder}
        draft={`teammate:${teammate.id}`}
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
          </span>
        }
      />
    </ComposerDock>
  )
}
