import { useState, type ReactNode } from "react"
import { Link } from "@tanstack/react-router"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  ArrowSquareOutIcon,
  BrainIcon,
  CaretDownIcon,
  CaretRightIcon,
  ChatCircleDotsIcon,
  CheckCircleIcon,
  ClockIcon,
  CubeIcon,
  ListChecksIcon,
  PlusIcon,
  SparkleIcon,
  TerminalWindowIcon,
  WarningIcon,
  type Icon,
} from "@phosphor-icons/react"
import {
  ApprovalCardList,
  useApprovalReview,
} from "@/components/approval-stack"
import {
  BrowsingCard,
  BrowsingNow,
  isBrowserAsk,
  useBrowsing,
} from "@/components/browser-browsing"
import { BrowserPanel } from "@/components/browser-panel"
import { ChatItemView } from "@/components/chat/chat-items"
import { Composer, type Prompt } from "@/components/chat/composer"
import { Markdown } from "@/components/chat/markdown"
import { PlanCard } from "@/components/chat/plan-card"
import { toolKind } from "@/components/chat/tool-call-card"
import { phase, phaseDot } from "@/components/status-badge"
import { StatusDot } from "@/components/status-dot"
import { PresenceDot, TeammateAvatar } from "@/components/teammate-avatar"
import { Readiness } from "@/components/teammate-readiness"
import { Bubble, BubbleContent } from "@/components/ui/bubble"
import { Button } from "@/components/ui/button"
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible"
import { Marker, MarkerContent, MarkerIcon } from "@/components/ui/marker"
import { Message, MessageAvatar, MessageContent } from "@/components/ui/message"
import {
  MessageScroller,
  MessageScrollerButton,
  MessageScrollerContent,
  MessageScrollerItem,
  MessageScrollerProvider,
  MessageScrollerViewport,
} from "@/components/ui/message-scroller"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Spinner } from "@/components/ui/spinner"
import { usePendingApprovals } from "@/hooks/use-pending-approvals"
import { presenceLabels, usePresence } from "@/hooks/use-teammate-presence"
import { tileClass } from "@/lib/accents"
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
import { approvalCards } from "@/lib/approval-groups"
import { chatTitle, useChat, type Chat, type ChatItem } from "@/lib/chats"
import { heartbeatSentence } from "@/lib/tasks"
import { harnessLabels, kindLabels } from "@/lib/teammates"
import { cn } from "@/lib/utils"

type Event = components["schemas"]["TeammateEvent"]
type Turn = components["schemas"]["TeammateTurn"]
type Mode = "message" | "task" | "teach"

const modes: { id: Mode; label: string; icon: Icon }[] = [
  { id: "message", label: "Message", icon: ChatCircleDotsIcon },
  { id: "task", label: "Task", icon: ListChecksIcon },
  { id: "teach", label: "Teach", icon: BrainIcon },
]

const timeFormat = new Intl.DateTimeFormat(undefined, {
  hour: "2-digit",
  minute: "2-digit",
})

function time(iso: string) {
  return timeFormat.format(new Date(iso))
}

function dayLabel(d: Date, now = new Date()) {
  const day = (x: Date) =>
    new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime()
  const diff = Math.round((day(now) - day(d)) / 86400000)
  if (diff === 0) return "Today"
  if (diff === 1) return "Yesterday"
  return d.toLocaleDateString(undefined, {
    weekday: "long",
    month: "short",
    day: "numeric",
  })
}

/** The approvals that wait on this teammate: its sandboxes', its chats', its memory's. */
function teammateApprovals(
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
        (a.sandboxId !== undefined && sandboxIds.has(a.sandboxId)))
  )
}

/**
 * The teammate page: one conversation with the teammate across its sandboxes and chats. The
 * thread shows its chat turns in brief, what it learned, its tasks and its terminal sessions
 * as their summaries tell them, with what waits for you at the end. A rail beside it shows
 * what it does now and what it knows.
 */
export function TeammateConversation({
  env,
  teammate,
}: {
  env: string
  teammate: Teammate
}) {
  const id = teammate.id
  const chats = $api.useQuery("get", "/api/environments/{env}/chats", {
    params: { path: { env } },
  })
  const sandboxes = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes",
    { params: { path: { env } } },
    { refetchInterval: 5000 }
  )
  const mine = (chats.data ?? []).filter((c) => c.teammateId === id)
  const running = mine.find((c) => c.state === "running")
  const timeline = $api.useQuery(
    "get",
    "/api/environments/{env}/teammates/{id}/timeline",
    { params: { path: { env, id } } },
    { refetchInterval: running ? 4000 : 30000 }
  )
  const live = useChat(env, running?.id)
  const owned = (sandboxes.data ?? []).filter((s) => s.teammateId === id)
  const pending = usePendingApprovals()
  const allWaiting = teammateApprovals(
    pending.data ?? [],
    env,
    id,
    new Set(owned.map((s) => s.id))
  )
  // Watching the browser beside the thread: its questions show on its page, not also here.
  const [watching, setWatching] = useState(false)
  const browserWaiting = allWaiting.some((a) => isBrowserAsk(a, id))
  const browsingNow = useBrowsing(env, id).now
  const waiting = watching
    ? allWaiting.filter((a) => !isBrowserAsk(a, id))
    : allWaiting

  return (
    <div className="flex min-h-0 flex-1">
      <div className="flex min-w-0 flex-1 flex-col">
        <Thread
          env={env}
          teammate={teammate}
          events={timeline.data ?? undefined}
          error={timeline.error ? errorMessage(timeline.error) : undefined}
          live={running ? live : undefined}
          waiting={waiting}
          multiple={owned.length > 1}
          browsing={
            browsingNow && (
              <BrowsingCard
                env={env}
                teammate={id}
                name={teammate.name}
                waiting={browserWaiting}
                watching={watching}
                onWatch={() => setWatching(true)}
              />
            )
          }
        />
        <TeammateComposer
          env={env}
          teammate={teammate}
          sandboxes={owned}
          chats={mine}
          events={timeline.data ?? []}
        />
      </div>
      {watching ? (
        <BrowserPanel
          env={env}
          teammate={id}
          storageKey="studio.panel.teammate"
          onClose={() => setWatching(false)}
        />
      ) : (
        <Rail
          env={env}
          teammate={teammate}
          sandboxes={owned}
          running={running}
          waiting={allWaiting.length}
          browserWaiting={browserWaiting}
          onWatch={() => setWatching(true)}
        />
      )}
    </div>
  )
}

function Thread({
  env,
  teammate,
  events,
  error,
  live,
  waiting,
  multiple,
  browsing,
}: {
  env: string
  teammate: Teammate
  events?: Event[]
  error?: string
  live?: ReturnType<typeof useChat>
  waiting: Approval[]
  multiple: boolean
  /** The browser's card, while the teammate browses: after the turns, before what waits. */
  browsing?: ReactNode
}) {
  const openTurn = live?.turns.find((t) => !t.endedAt)
  const known = new Set(
    (events ?? []).flatMap((e) => (e.turn ? [e.turn.turnId] : []))
  )
  const rows: ReactNode[] = []
  let day = ""
  let where = ""
  for (const e of events ?? []) {
    const d = new Date(e.at)
    if (d.toDateString() !== day) {
      day = d.toDateString()
      rows.push(<DayDivider key={`day-${e.at}`} label={dayLabel(d)} />)
    }
    if (e.sandboxName && e.sandboxName !== where) {
      if (where || multiple) {
        rows.push(
          <WhereDivider
            key={`where-${e.at}-${e.sandboxName}`}
            name={teammate.name}
            sandbox={e.sandboxName}
          />
        )
      }
      where = e.sandboxName
    }
    const key = `${e.kind}-${e.turn?.turnId ?? e.fact?.id ?? e.task?.id ?? e.session?.id}-${e.task?.event ?? ""}`
    if (e.turn && live && openTurn?.id === e.turn.turnId) {
      rows.push(
        <LiveTurn
          key={key}
          env={env}
          teammate={teammate}
          chat={live.chat}
          items={live.items.filter((i) => i.turnId === openTurn.id)}
        />
      )
      continue
    }
    rows.push(<EventRow key={key} env={env} teammate={teammate} event={e} />)
  }
  // A turn that started after the timeline was read.
  if (live?.chat && openTurn && !known.has(openTurn.id)) {
    rows.push(
      <LiveTurn
        key={`live-${openTurn.id}`}
        env={env}
        teammate={teammate}
        chat={live.chat}
        items={live.items.filter((i) => i.turnId === openTurn.id)}
      />
    )
  }

  return (
    <MessageScrollerProvider defaultScrollPosition="end" autoScroll>
      <MessageScroller className="min-h-0 flex-1">
        <MessageScrollerViewport
          aria-label={`Conversation with ${teammate.name}`}
        >
          <MessageScrollerContent className="mx-auto w-full max-w-3xl gap-3 p-4">
            {!events && !error && (
              <MessageScrollerItem className="m-auto">
                <Spinner />
              </MessageScrollerItem>
            )}
            {error && (
              <MessageScrollerItem>
                <p className="text-xs text-destructive">{error}</p>
              </MessageScrollerItem>
            )}
            {events?.length === 0 && !live && (
              <MessageScrollerItem className="m-auto flex max-w-sm flex-col items-center text-center text-xs text-muted-foreground">
                <TeammateAvatar
                  name={teammate.name}
                  avatar={teammate.avatar}
                  className="mx-auto mb-2 size-12 text-base"
                />
                Nothing between you and {teammate.name} in the last two weeks.
                Send a message, hand over a task, or teach it something to
                remember.
              </MessageScrollerItem>
            )}
            {rows.map((row, i) => (
              <MessageScrollerItem key={i}>{row}</MessageScrollerItem>
            ))}
            {browsing && <MessageScrollerItem>{browsing}</MessageScrollerItem>}
            {waiting.length > 0 && (
              <MessageScrollerItem>
                <Waiting approvals={waiting} env={env} />
              </MessageScrollerItem>
            )}
          </MessageScrollerContent>
        </MessageScrollerViewport>
        <MessageScrollerButton />
      </MessageScroller>
    </MessageScrollerProvider>
  )
}

function DayDivider({ label }: { label: string }) {
  return (
    <div className="flex items-center gap-3 py-1 font-mono text-[0.625rem] tracking-wider text-muted-foreground uppercase">
      <span className="h-px flex-1 bg-border" />
      {label}
      <span className="h-px flex-1 bg-border" />
    </div>
  )
}

/** Where the work moves to another sandbox: each one is its own machine. */
function WhereDivider({ name, sandbox }: { name: string; sandbox: string }) {
  return (
    <div className="flex justify-center text-xs">
      <span className="inline-flex items-center gap-1.5 rounded-full border bg-card px-2.5 py-0.5">
        <CubeIcon className="size-3.5 text-muted-foreground" />
        {name} in <span className="font-mono font-medium">{sandbox}</span>
      </span>
    </div>
  )
}

function EventRow({
  env,
  teammate,
  event: e,
}: {
  env: string
  teammate: Teammate
  event: Event
}) {
  if (e.turn)
    return <TurnRow env={env} teammate={teammate} at={e.at} turn={e.turn} />
  if (e.fact) return <Learned env={env} at={e.at} fact={e.fact} />
  if (e.task) {
    const done = e.task.event === "done"
    return (
      <Activity
        icon={done ? CheckCircleIcon : ListChecksIcon}
        at={e.at}
        link={
          <Link
            to="/e/$env/teammates/$id/tasks"
            params={{ env, id: teammate.id }}
            className="hover:text-foreground"
          >
            Tasks
          </Link>
        }
      >
        {done
          ? "Finished a task: "
          : e.task.createdBy === "teammate"
            ? "Added a task for itself: "
            : "Task added: "}
        <span className="font-medium">{e.task.title}</span>
      </Activity>
    )
  }
  if (e.session) {
    return (
      <div className="ml-10 grid gap-1 rounded-lg border bg-card px-3 py-2 text-xs">
        <span className="flex items-center gap-1.5 font-mono text-[0.625rem] tracking-wider text-muted-foreground uppercase">
          <TerminalWindowIcon className="size-3" />
          In the terminal · {time(e.at)}
        </span>
        <span>{e.session.summary}</span>
      </div>
    )
  }
  return null
}

/** A finished turn in brief: the prompt, the tools used, the answer. */
function TurnRow({
  env,
  teammate,
  at,
  turn,
}: {
  env: string
  teammate: Teammate
  at: string
  turn: Turn
}) {
  const open = (
    <Link
      to="/e/$env/chats/$chat"
      params={{ env, chat: turn.chatId }}
      className="inline-flex items-center gap-0.5 hover:text-foreground"
      title={`Open the chat ${turn.chatTitle || ""}`.trim()}
    >
      Chat
      <ArrowSquareOutIcon className="size-3" />
    </Link>
  )
  return (
    <div className="grid gap-2">
      {turn.heartbeat ? (
        <span className="ml-10 flex items-center gap-1.5 font-mono text-[0.625rem] tracking-wider text-muted-foreground uppercase">
          <ClockIcon className="size-3" />
          Check-in · {time(at)}
        </span>
      ) : (
        turn.prompt && (
          <Message align="end">
            <MessageContent>
              <Bubble align="end" variant="secondary">
                <BubbleContent className="whitespace-pre-wrap">
                  {turn.prompt}
                </BubbleContent>
              </Bubble>
              <span className="self-end font-mono text-[0.625rem] text-muted-foreground">
                {turn.origin === "terminal" && "in the terminal · "}
                {time(at)}
              </span>
            </MessageContent>
          </Message>
        )
      )}
      {turn.toolCount > 0 && (
        <Tools turn={turn} link={turn.answer ? undefined : open} />
      )}
      {turn.answer && (
        <Answer teammate={teammate} text={turn.answer} link={open} />
      )}
      {turn.marker && (
        <Marker variant="separator" className="text-destructive">
          <MarkerIcon>
            <WarningIcon />
          </MarkerIcon>
          <MarkerContent>
            {turn.markerText ||
              {
                stopped: "Stopped",
                interrupted: "Interrupted",
                error: "Something went wrong",
                limit: "Hit a limit",
              }[turn.marker]}
          </MarkerContent>
        </Marker>
      )}
    </div>
  )
}

function Answer({
  teammate,
  text,
  link,
}: {
  teammate: Teammate
  text: string
  link?: ReactNode
}) {
  return (
    <Message>
      <MessageAvatar className="min-w-0 bg-transparent">
        <TeammateAvatar
          name={teammate.name}
          avatar={teammate.avatar}
          className="size-8 text-xs"
        />
      </MessageAvatar>
      <MessageContent>
        <Bubble variant="muted">
          <BubbleContent>
            <Markdown text={text} />
          </BubbleContent>
        </Bubble>
        {link && (
          <span className="text-[0.625rem] text-muted-foreground">{link}</span>
        )}
      </MessageContent>
    </Message>
  )
}

/** The tools of a turn on one line; it opens to their titles. */
function Tools({ turn, link }: { turn: Turn; link?: ReactNode }) {
  const tools = turn.tools ?? []
  const last = tools.at(-1)
  const failed = tools.filter((t) => t.status === "failed").length
  const K = toolKind(last?.kind)
  return (
    <Collapsible className="group/tools ml-10 grid gap-1 text-xs">
      <div className="flex min-w-0 items-center gap-2">
        <span
          className={cn(
            "inline-flex size-5 shrink-0 items-center justify-center rounded-[4px]",
            tileClass("chat")
          )}
        >
          <K.icon weight="bold" className="size-3" />
        </span>
        <CollapsibleTrigger className="flex min-w-0 items-center gap-1 text-left outline-none hover:text-foreground">
          <CaretRightIcon className="size-3 shrink-0 transition-transform group-data-[open]/tools:rotate-90" />
          <span className="shrink-0">
            Used {turn.toolCount} {turn.toolCount === 1 ? "tool" : "tools"}
            {failed > 0 && `, ${failed} failed`}
          </span>
          {last && (
            <span className="truncate text-muted-foreground">
              · {last.title}
            </span>
          )}
        </CollapsibleTrigger>
        <span className="ml-auto shrink-0 text-[0.625rem] text-muted-foreground">
          {link}
        </span>
      </div>
      <CollapsibleContent>
        <ol className="ml-7 grid gap-0.5 border-l pl-3 font-mono text-[0.6875rem] text-muted-foreground">
          {turn.toolCount > tools.length && (
            <li>… {turn.toolCount - tools.length} before these</li>
          )}
          {tools.map((t, i) => (
            <li
              key={i}
              className={cn(
                "truncate",
                t.status === "failed" && "text-destructive"
              )}
            >
              {t.title}
            </li>
          ))}
        </ol>
      </CollapsibleContent>
    </Collapsible>
  )
}

/** The turn underway, from the chat's stream: what it says, the tool it runs, what it asks. */
function LiveTurn({
  env,
  teammate,
  chat,
  items,
}: {
  env: string
  teammate: Teammate
  chat?: Chat
  items: ChatItem[]
}) {
  const lastTool = [...items].reverse().find((i) => i.kind === "tool")
  const plan = [...items].reverse().find((i) => i.kind === "plan")
  return (
    <div className="grid gap-2">
      {items.map((it) => {
        switch (it.kind) {
          case "user":
            return (
              <Message key={it.id} align="end">
                <MessageContent>
                  <Bubble align="end" variant="secondary">
                    <BubbleContent className="whitespace-pre-wrap">
                      {it.user?.text}
                    </BubbleContent>
                  </Bubble>
                </MessageContent>
              </Message>
            )
          case "agent":
            return it.text?.text ? (
              <Answer key={it.id} teammate={teammate} text={it.text.text} />
            ) : null
          case "permission":
          case "question":
          case "marker":
            return chat ? (
              <ChatItemView
                key={it.id}
                item={it}
                ctx={{
                  env,
                  chatId: chat.id,
                  teammateName: teammate.name,
                  live: true,
                }}
              />
            ) : null
          default:
            return null
        }
      })}
      {plan?.plan && <PlanCard entries={plan.plan.entries ?? []} live />}
      <div className="ml-10 flex min-w-0 items-center gap-2 text-xs text-muted-foreground">
        <Spinner className="size-3" />
        <span className="shimmer truncate">
          {lastTool?.tool
            ? `${lastTool.tool.title}…`
            : `${teammate.name} is working…`}
        </span>
        {chat && (
          <Link
            to="/e/$env/chats/$chat"
            params={{ env, chat: chat.id }}
            className="ml-auto inline-flex shrink-0 items-center gap-0.5 text-[0.625rem] hover:text-foreground"
          >
            Watch in chat
            <ArrowSquareOutIcon className="size-3" />
          </Link>
        )}
      </div>
    </div>
  )
}

function Activity({
  icon: I,
  at,
  link,
  children,
}: {
  icon: Icon
  at: string
  link?: ReactNode
  children: ReactNode
}) {
  return (
    <div className="ml-10 flex min-w-0 items-center gap-2 text-xs">
      <span
        className={cn(
          "inline-flex size-5 shrink-0 items-center justify-center rounded-[4px]",
          tileClass("chat")
        )}
      >
        <I weight="bold" className="size-3" />
      </span>
      <span className="min-w-0 truncate">{children}</span>
      <span className="ml-auto flex shrink-0 items-center gap-2 font-mono text-[0.625rem] text-muted-foreground">
        {link}
        {time(at)}
      </span>
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
    ]) {
      void queryClient.invalidateQueries({ queryKey: ["get", path] })
    }
  }
  return (
    <div
      className={cn(
        "ml-10 flex items-start gap-2.5 rounded-lg border border-dashed border-(--acc-memory)/50 bg-(--acc-memory)/5 px-3 py-2 text-xs",
        forgotten && "opacity-60"
      )}
    >
      <SparkleIcon
        weight="fill"
        className="mt-0.5 size-3.5 shrink-0 text-(--acc-memory)"
      />
      <div className="grid min-w-0 flex-1 gap-0.5">
        <span className="font-mono text-[0.625rem] tracking-wider text-(--acc-memory) uppercase">
          {forgotten
            ? "Forgotten"
            : fact.tier === "user"
              ? "Learned from you"
              : "Learned"}{" "}
          · {time(at)}
        </span>
        <span className={cn(forgotten && "line-through")}>{fact.text}</span>
      </div>
      {!forgotten && (
        <Button
          size="xs"
          variant="ghost"
          className="shrink-0 text-muted-foreground"
          disabled={busy}
          onClick={() => void forget()}
        >
          Forget
        </Button>
      )}
    </div>
  )
}

/** What waits for you on this teammate, as the approval cards elsewhere show it. */
function Waiting({ env, approvals }: { env: string; approvals: Approval[] }) {
  useClaimApprovals(approvals.map((a) => a.id))
  const [split, setSplit] = useState<ReadonlySet<string>>(() => new Set())
  const envs = $api.useQuery("get", "/api/environments")
  const { sheet, review } = useApprovalReview(approvals)
  const cards = approvalCards(approvals, split)
  return (
    <section aria-label="Waiting for you" className="ml-10 grid gap-2">
      <span className="font-mono text-[0.625rem] tracking-wider text-muted-foreground uppercase">
        Waiting for you
      </span>
      <ApprovalCardList
        cards={cards}
        max={3}
        envName={() => envs.data?.find((e) => e.id === env)?.name}
        onReview={review}
        onSplit={(ids) => setSplit((prev) => new Set([...prev, ...ids]))}
      />
      {sheet}
    </section>
  )
}

// --- the composer --------------------------------------------------------------------------

/**
 * Message continues the teammate's latest chat in the chosen sandbox, or starts one; Task adds
 * a task and starts a chat to work on it; Teach writes a fact to its memory, with no chat.
 */
function TeammateComposer({
  env,
  teammate,
  sandboxes,
  chats,
  events,
}: {
  env: string
  teammate: Teammate
  sandboxes: Sandbox[]
  chats: Chat[]
  events: Event[]
}) {
  const queryClient = useQueryClient()
  const [mode, setMode] = useState<Mode>("message")
  const [fresh, setFresh] = useState(false)
  const [problem, setProblem] = useState<string>()
  const lastSandbox = [...events].reverse().find((e) => e.turn)?.sandboxId
  const [picked, setPicked] = useState<string>()
  const sandbox =
    sandboxes.find((s) => s.id === picked) ??
    sandboxes.find((s) => s.id === lastSandbox) ??
    sandboxes.at(0)
  const current = chats
    .filter(
      (c) =>
        c.sandboxId === sandbox?.id &&
        !c.heartbeat &&
        (c.state === "idle" || c.state === "running")
    )
    .sort((a, b) => b.updatedAt.localeCompare(a.updatedAt))
    .at(0)
  const continuing = mode === "message" && !fresh ? current : undefined

  const refresh = () => {
    for (const path of [
      "/api/environments/{env}/teammates/{id}/timeline",
      "/api/environments/{env}/chats",
      "/api/environments/{env}/teammates/{id}/tasks",
      "/api/environments/{env}/memory/facts",
    ]) {
      void queryClient.invalidateQueries({ queryKey: ["get", path] })
    }
  }

  const startChat = async (body: Prompt) => {
    if (!sandbox)
      throw new Error(`${teammate.name} has no sandbox to work in yet.`)
    const { error } = await fetchClient.POST(
      "/api/environments/{env}/sandboxes/{id}/chats",
      { params: { path: { env, id: sandbox.id } }, body }
    )
    if (error) throw error
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
      } else if (mode === "task") {
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
        await startChat({
          text: `I added a task to your list: "${title}".${details ? `\n\n${details}` : ""}\n\nPlease work on it now: mark it in progress, and done when you finish.`,
        })
      } else if (continuing) {
        const { error } = await fetchClient.POST(
          "/api/environments/{env}/chats/{chat}/prompt",
          { params: { path: { env, chat: continuing.id } }, body: p }
        )
        if (error) throw error
      } else {
        await startChat(p)
        setFresh(false)
      }
      refresh()
      return true
    } catch (err) {
      setProblem(errorMessage(err))
      return false
    }
  }

  const stop = () => {
    if (!continuing) return
    void fetchClient
      .POST("/api/environments/{env}/chats/{chat}/cancel", {
        params: { path: { env, chat: continuing.id } },
      })
      .then((res) => {
        if (res.error) toast.error(errorMessage(res.error))
      })
  }

  const placeholder = {
    message: continuing
      ? `Message ${teammate.name}…`
      : `Message ${teammate.name}; this starts a new chat…`,
    task: `Hand ${teammate.name} a task: the first line is its title. It starts on it right away.`,
    teach: `Tell ${teammate.name} something to remember for good…`,
  }[mode]
  const hint =
    mode === "teach" ? (
      `Saved to ${teammate.name}'s memory, not sent as a message.`
    ) : mode === "message" && continuing ? (
      <>
        in{" "}
        <Link
          to="/e/$env/chats/$chat"
          params={{ env, chat: continuing.id }}
          className="underline-offset-2 hover:underline"
        >
          {chatTitle(continuing)}
        </Link>
      </>
    ) : undefined

  return (
    <div className="mx-auto w-full max-w-3xl shrink-0 px-4 pb-4">
      <Composer
        onSend={send}
        onStop={stop}
        running={continuing?.state === "running"}
        error={problem}
        placeholder={placeholder}
        attachments={mode === "message"}
        hint={hint}
        className={cn(
          mode === "teach" && "border-(--acc-memory)/60",
          mode === "task" && "border-(--acc-chat)/60"
        )}
        toolbar={
          <div className="flex flex-wrap items-center gap-1 border-b pb-1.5">
            {modes.map((m) => (
              <button
                key={m.id}
                type="button"
                aria-pressed={mode === m.id}
                onClick={() => setMode(m.id)}
                className={cn(
                  "inline-flex h-6 items-center gap-1.5 rounded-md px-2 text-xs",
                  mode === m.id
                    ? "bg-muted font-medium"
                    : "text-muted-foreground hover:text-foreground"
                )}
              >
                <m.icon className="size-3.5" />
                {m.label}
              </button>
            ))}
            {mode === "message" && current && (
              <button
                type="button"
                aria-pressed={fresh}
                onClick={() => setFresh(!fresh)}
                title="Start a new chat instead of continuing the last one"
                className={cn(
                  "inline-flex h-6 items-center gap-1 rounded-md px-2 text-xs",
                  fresh
                    ? "bg-muted font-medium"
                    : "text-muted-foreground hover:text-foreground"
                )}
              >
                <PlusIcon className="size-3.5" />
                New chat
              </button>
            )}
            {mode !== "teach" && sandboxes.length > 0 && (
              <SandboxPicker
                sandboxes={sandboxes}
                value={sandbox}
                onChange={setPicked}
              />
            )}
          </div>
        }
      />
    </div>
  )
}

function SandboxPicker({
  sandboxes,
  value,
  onChange,
}: {
  sandboxes: Sandbox[]
  value?: Sandbox
  onChange: (id: string) => void
}) {
  return (
    <label className="ml-auto inline-flex h-6 items-center gap-1 rounded-md px-2 text-xs text-muted-foreground">
      <CubeIcon className="size-3.5" />
      in
      <span className="relative inline-flex items-center">
        <select
          value={value?.id}
          onChange={(e) => onChange(e.target.value)}
          aria-label="Sandbox"
          className="cursor-pointer appearance-none bg-transparent pr-4 font-mono text-foreground outline-none"
        >
          {sandboxes.map((s) => (
            <option key={s.id} value={s.id}>
              {s.name}
            </option>
          ))}
        </select>
        <CaretDownIcon className="pointer-events-none absolute right-0 size-3" />
      </span>
    </label>
  )
}

// --- the rail ------------------------------------------------------------------------------

function Rail({
  env,
  teammate,
  sandboxes,
  running,
  waiting,
  browserWaiting,
  onWatch,
}: {
  env: string
  teammate: Teammate
  sandboxes: Sandbox[]
  running?: Chat
  waiting: number
  browserWaiting: boolean
  onWatch: () => void
}) {
  const presence = usePresence(env)(teammate.id)
  const [soul, setSoul] = useState(false)
  const providers = $api.useQuery("get", "/api/environments/{env}/providers", {
    params: { path: { env } },
  })
  const facts = $api.useQuery("get", "/api/environments/{env}/memory/facts", {
    params: {
      path: { env },
      query: { scope: `teammate:${teammate.id}`, status: "active" },
    },
  })
  const tasks = $api.useQuery(
    "get",
    "/api/environments/{env}/teammates/{id}/tasks",
    { params: { path: { env, id: teammate.id } } }
  )
  const provider = providers.data?.find((p) => p.id === teammate.providerId)
  const open = (tasks.data ?? []).filter((t) => t.status !== "done")
  const known = facts.data ?? []

  return (
    <aside className="w-72 shrink-0 border-l max-lg:hidden">
      <ScrollArea className="h-full">
        <div className="grid gap-5 p-4 text-xs">
          <div className="grid justify-items-center gap-2 pt-2 text-center">
            <TeammateAvatar
              name={teammate.name}
              avatar={teammate.avatar}
              className="size-14 text-lg"
            />
            <div>
              <div className="text-sm font-semibold">{teammate.name}</div>
              {teammate.role && (
                <div className="text-muted-foreground">{teammate.role}</div>
              )}
            </div>
            <span className="inline-flex items-center gap-1.5 rounded-full border px-2 py-0.5">
              <PresenceDot presence={presence} />
              {presenceLabels[presence]}
            </span>
          </div>

          <RailSection title="Now">
            <BrowsingNow
              env={env}
              teammate={teammate.id}
              waiting={browserWaiting}
              onWatch={onWatch}
            />
            {running ? (
              <Link
                to="/e/$env/chats/$chat"
                params={{ env, chat: running.id }}
                className="flex items-center gap-1.5 hover:underline"
              >
                <Spinner className="size-3" />
                <span className="truncate">{chatTitle(running)}</span>
              </Link>
            ) : waiting > 0 ? (
              <p>
                Waiting for you: {waiting}{" "}
                {waiting === 1 ? "approval" : "approvals"}, at the end of the
                conversation.
              </p>
            ) : open.length > 0 ? (
              <ul className="grid gap-1">
                {open.slice(0, 3).map((t) => (
                  <li key={t.id} className="flex items-center gap-1.5">
                    <span
                      className={cn(
                        "size-2.5 shrink-0 rounded-full border",
                        t.status === "in_progress" &&
                          "border-(--st-running) bg-(--st-running)"
                      )}
                    />
                    <span className="truncate">{t.title}</span>
                  </li>
                ))}
              </ul>
            ) : (
              <p className="text-muted-foreground">Nothing on its plate.</p>
            )}
          </RailSection>

          <RailSection
            title="Knows"
            count={known.length}
            action={
              <Link
                to="/e/$env/teammates/$id/memory"
                params={{ env, id: teammate.id }}
              >
                Memory
              </Link>
            }
          >
            {known.length === 0 ? (
              <p className="text-muted-foreground">
                Nothing yet. Teach it something below.
              </p>
            ) : (
              <ul className="grid gap-1.5">
                {known.slice(0, 4).map((f) => (
                  <li key={f.id} className="flex gap-1.5">
                    <SparkleIcon
                      weight="fill"
                      className="mt-0.5 size-3 shrink-0 text-(--acc-memory)"
                    />
                    <span className="line-clamp-2">{f.text}</span>
                  </li>
                ))}
              </ul>
            )}
          </RailSection>

          <RailSection
            title="Routines"
            action={
              <Link
                to="/e/$env/teammates/$id/settings"
                params={{ env, id: teammate.id }}
              >
                Edit
              </Link>
            }
          >
            <p className="flex items-center gap-1.5">
              <ClockIcon className="size-3.5 shrink-0 text-muted-foreground" />
              {teammate.heartbeat
                ? `Checks in on open tasks. ${heartbeatSentence(teammate.heartbeat)}.`
                : "No check-ins."}
            </p>
          </RailSection>

          <RailSection title="Sandboxes" count={sandboxes.length}>
            {sandboxes.length === 0 ? (
              <p className="text-muted-foreground">
                None. Pick {teammate.name} as the owner when you create one.
              </p>
            ) : (
              <div className="flex flex-wrap gap-1.5">
                {sandboxes.map((s) => (
                  <Link
                    key={s.id}
                    to="/e/$env/sandboxes/$id"
                    params={{ env, id: s.id }}
                    className="inline-flex items-center gap-1.5 rounded-md border px-2 py-0.5 font-mono hover:bg-muted"
                  >
                    <StatusDot {...phaseDot(phase(s))} compact />
                    {s.name}
                  </Link>
                ))}
              </div>
            )}
          </RailSection>

          <RailSection
            title="Profile"
            action={
              <Link
                to="/e/$env/teammates/$id/settings"
                params={{ env, id: teammate.id }}
              >
                Edit
              </Link>
            }
          >
            <dl className="grid grid-cols-[4.5rem_1fr] gap-y-1">
              <dt className="text-muted-foreground">Runs in</dt>
              <dd>{harnessLabels[teammate.harness]}</dd>
              <dt className="text-muted-foreground">Provider</dt>
              <dd className="truncate">
                {provider
                  ? `${provider.name} (${kindLabels[provider.kind]})`
                  : "None"}
              </dd>
              {teammate.model && (
                <>
                  <dt className="text-muted-foreground">Model</dt>
                  <dd className="truncate font-mono">{teammate.model}</dd>
                </>
              )}
              <dt className="text-muted-foreground">Status</dt>
              <dd>
                <Readiness teammate={teammate} provider={provider} />
              </dd>
            </dl>
            {teammate.soul && (
              <>
                <button
                  type="button"
                  onClick={() => setSoul(!soul)}
                  aria-expanded={soul}
                  className="flex items-center gap-1 text-muted-foreground hover:text-foreground"
                >
                  <CaretDownIcon
                    className={cn(
                      "size-3 transition-transform",
                      soul && "rotate-180"
                    )}
                  />
                  Soul
                </button>
                {soul && (
                  <pre className="font-mono text-[0.6875rem]/relaxed break-words whitespace-pre-wrap text-muted-foreground">
                    {teammate.soul}
                  </pre>
                )}
              </>
            )}
          </RailSection>
        </div>
      </ScrollArea>
    </aside>
  )
}

function RailSection({
  title,
  count,
  action,
  children,
}: {
  title: string
  count?: number
  action?: ReactNode
  children: ReactNode
}) {
  return (
    <section className="grid gap-2">
      <div className="flex items-center gap-2">
        <h2 className="font-mono text-[0.625rem] tracking-wider text-muted-foreground uppercase">
          {title}
        </h2>
        {count !== undefined && (
          <span className="font-mono text-[0.625rem] text-muted-foreground">
            {count}
          </span>
        )}
        {action && (
          <span className="ml-auto text-[0.6875rem] text-muted-foreground hover:text-foreground">
            {action}
          </span>
        )}
      </div>
      {children}
    </section>
  )
}
