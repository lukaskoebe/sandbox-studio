import { useState, type ReactNode } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  BrainIcon,
  CaretDownIcon,
  ChatCircleDotsIcon,
  ChatCircleIcon,
  CheckCircleIcon,
  ClockIcon,
  ListChecksIcon,
  SparkleIcon,
  TerminalWindowIcon,
  type Icon,
} from "@phosphor-icons/react"
import {
  ApprovalCardList,
  useApprovalReview,
} from "@/components/approval-stack"
import { BrowsingCard, useBrowsing } from "@/components/browser-browsing"
import { isGitApproval } from "@/components/git-review"
import { ComposerDock } from "@/components/chat/chat-thread"
import { Composer, type Prompt } from "@/components/chat/composer"
import { TeammateHeader } from "@/components/teammate/header"
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

/**
 * The teammate's home: its threads as cards by day, what it learned, its tasks and terminal
 * work between them, what waits for you at the end, and a composer that starts a new thread on
 * the computer on the screen.
 */
export function TeammateHome({
  env,
  teammate,
  chats,
  computers,
  computer,
  waiting,
  watching,
  onThread,
  onProfile,
  onWatch,
  onReview,
}: {
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
  onThread: (chat: Chat) => void
  onProfile: (view: "card" | "tasks" | "memory") => void
  onWatch: () => void
  /** Opens a push waiting for review on the screen's Changes. */
  onReview: (a: Approval) => void
}) {
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

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <TeammateHeader env={env} teammate={teammate} onProfile={onProfile} />
      <MessageScrollerProvider defaultScrollPosition="end" autoScroll>
        <MessageScroller className="min-h-0 flex-1">
          <MessageScrollerViewport aria-label={`Threads with ${teammate.name}`}>
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
        onThread={onThread}
      />
    </div>
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
      className="grid w-full gap-1.5 rounded-xl border bg-card px-3.5 py-3 text-left shadow-xs transition-[border-color,box-shadow] outline-none hover:border-foreground/20 hover:shadow-sm focus-visible:ring-2 focus-visible:ring-ring/40"
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
          <span className="shrink-0 rounded-full border px-2 font-mono text-caption text-muted-foreground">
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
  computer,
  onThread,
}: {
  env: string
  teammate: Teammate
  computer?: Sandbox
  onThread: (chat: Chat) => void
}) {
  const queryClient = useQueryClient()
  const [mode, setMode] = useState<Mode>("message")
  const [problem, setProblem] = useState<string>()

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
      { params: { path: { env, id: computer.id } }, body }
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
          ? `Added to the tasks, then a new thread on ${computer.name}`
          : `Starts a new thread on ${computer.name}`
        : `${teammate.name} needs a computer first`

  return (
    <ComposerDock className="max-w-2xl">
      <Composer
        onSend={send}
        error={problem}
        placeholder={placeholder}
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
            <span className="truncate">{where}</span>
          </span>
        }
      />
    </ComposerDock>
  )
}
