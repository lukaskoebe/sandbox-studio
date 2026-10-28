import {
  Fragment,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react"
import { Link } from "@tanstack/react-router"
import { toast } from "sonner"
import {
  ArrowUpIcon,
  ChatCircleDotsIcon,
  TerminalWindowIcon,
} from "@phosphor-icons/react"
import {
  ChatItemView,
  SendingMessage,
  turnUsage,
} from "@/components/chat/chat-items"
import { Composer, type Prompt } from "@/components/chat/composer"
import { PreviewSandbox } from "@/components/chat/markdown"
import { OpenWorkspaceFile } from "@/components/chat/mention-picker"
import { PlanCard } from "@/components/chat/plan-card"
import { QueuedPrompts } from "@/components/chat/queued-prompts"
import { TurnFooter } from "@/components/chat/turn-footer"
import { SandboxStateBanner } from "@/components/sandbox-lifecycle"
import { Button } from "@/components/ui/button"
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty"
import {
  MessageScroller,
  MessageScrollerButton,
  MessageScrollerContent,
  MessageScrollerItem,
  MessageScrollerProvider,
  MessageScrollerViewport,
} from "@/components/ui/message-scroller"
import { Spinner } from "@/components/ui/spinner"
import { $api, errorMessage, fetchClient } from "@/lib/api/client"
import {
  changeCount,
  type Chat,
  type ChatItem,
  type ChatTurn,
  type QueuedPrompt,
  type useChat,
} from "@/lib/chats"
import { usePreviewSetting } from "@/lib/preview-setting"
import { cn } from "@/lib/utils"

/**
 * A request's name in lists and dialogs: the start of its message, from its header or, for a
 * turn the stream brought, its items.
 */
export function requestLabel(items: ChatItem[], t: ChatTurn) {
  const text = (
    t.prompt ??
    items.find((i) => i.turnId === t.id && i.kind === "user")?.user?.text
  )?.trim()
  if (!text) return t.origin === "terminal" ? "In the terminal" : "A message"
  return text.length > 60 ? `${text.slice(0, 60)}…` : text
}

/**
 * A chat's messages and composer, from the log of useChat: a thread on the teammate's page, or
 * the chat page of one the list doesn't know.
 */
export function ChatThread({
  env,
  chat,
  log,
  onShowChanges,
  onOpenFile,
}: {
  env: string
  chat: Chat
  log: ReturnType<typeof useChat>
  onShowChanges: (turnId: string) => void
  /** Opens a file a message mentions beside the chat; without, mentions link to the sandbox. */
  onOpenFile?: (path: string) => void
}) {
  const teammates = $api.useQuery("get", "/api/environments/{env}/teammates", {
    params: { path: { env } },
  })
  const teammateName =
    teammates.data?.find((t) => t.id === chat.teammateId)?.name ?? "Teammate"
  return (
    <ChatLog
      env={env}
      chat={chat}
      items={log.items}
      turns={log.turns}
      queued={log.queued}
      teammateName={teammateName}
      label={(t) => requestLabel(log.items, t)}
      onShowChanges={onShowChanges}
      onOpenFile={onOpenFile}
      earlier={log.earlier}
      loadingEarlier={log.loadingEarlier}
      onLoadEarlier={log.loadEarlier}
    />
  )
}

function ChatLog({
  env,
  chat,
  items,
  turns,
  queued,
  teammateName,
  label,
  onShowChanges,
  onOpenFile,
  earlier,
  loadingEarlier,
  onLoadEarlier,
}: {
  env: string
  chat: Chat
  items: ChatItem[]
  turns: ChatTurn[]
  queued: QueuedPrompt[]
  teammateName: string
  label: (t: ChatTurn) => string
  onShowChanges: (turnId: string) => void
  onOpenFile?: (path: string) => void
  /** The turns whose items aren't loaded yet. */
  earlier: ChatTurn[]
  loadingEarlier: boolean
  onLoadEarlier: () => Promise<void>
}) {
  const turnById = new Map(turns.map((t) => [t.id, t]))
  const running = chat.state === "running"
  // The turn still underway, if any: its last item is the one still being written,
  // and its latest plan stays pinned above the composer.
  const open = running
    ? [...turns].reverse().find((t) => !t.endedAt)
    : undefined
  const openItems = open ? items.filter((i) => i.turnId === open.id) : []
  const liveId = openItems.at(-1)?.id
  const pinned = [...openItems].reverse().find((i) => i.kind === "plan")

  // The message being sent shows until the log has it as a message or a queued prompt.
  const users = items.filter((i) => i.kind === "user").length
  // A new message scrolls to the top. Only the latest is an anchor: the scroller goes to
  // anchors it hasn't gone to yet, and would jump back to one from when the page opened.
  const lastUser = [...items].reverse().find((i) => i.kind === "user")?.id
  const [sending, setSending] = useState<Sending>()
  if (sending && (users > sending.users || queued.length > sending.queued))
    setSending(undefined)
  const onSending = (p: Prompt | undefined) =>
    setSending(
      p && {
        prompt: p,
        users,
        queued: queued.length,
        key: Date.now(),
      }
    )
  // A message the server took but the stream didn't bring stops showing after a while; the
  // log has it once the stream is back.
  useEffect(() => {
    if (!sending?.sent) return
    const t = setTimeout(() => setSending(undefined), 15_000)
    return () => clearTimeout(t)
  }, [sending?.sent])

  const { enabled: previewsOn } = usePreviewSetting()
  const previews = useMemo(
    () => ({ env, sandboxId: chat.sandboxId, enabled: previewsOn }),
    [env, chat.sandboxId, previewsOn]
  )

  return (
    <PreviewSandbox.Provider value={previews}>
      <OpenWorkspaceFile.Provider value={onOpenFile ?? null}>
        <MessageScrollerProvider defaultScrollPosition="end" autoScroll>
          <div className="flex min-h-0 min-w-0 flex-1 flex-col">
            <MessageScroller className="min-h-0 flex-1">
              <MessageScrollerViewport
                aria-label="Messages"
                preserveScrollOnPrepend
              >
                {/* Outside the content: the scroller takes every child of it for a message, and
                  keeps the reading place only when its first one moves down. */}
                {earlier.length > 0 && (
                  <div className="mx-auto w-full max-w-3xl px-4 pt-4">
                    <LoadEarlier
                      turns={earlier}
                      loading={loadingEarlier}
                      onLoad={onLoadEarlier}
                    />
                  </div>
                )}
                <MessageScrollerContent className="mx-auto w-full max-w-3xl gap-4 p-4">
                  {items.length === 0 && earlier.length === 0 && !sending && (
                    <MessageScrollerItem className="m-auto">
                      <Empty>
                        <EmptyHeader>
                          <EmptyMedia variant="icon">
                            <ChatCircleDotsIcon />
                          </EmptyMedia>
                          <EmptyTitle>Nothing here yet</EmptyTitle>
                          <EmptyDescription>
                            Say what {teammateName} should do.
                          </EmptyDescription>
                        </EmptyHeader>
                      </Empty>
                    </MessageScrollerItem>
                  )}
                  {items.map((item, i) => {
                    const turn = turnById.get(item.turnId)
                    const lastOfTurn = items.at(i + 1)?.turnId !== item.turnId
                    const footer =
                      lastOfTurn &&
                      turn?.endedAt &&
                      (changeCount(turn) > 0 || turnUsage(turn))
                        ? turn
                        : undefined
                    // The note that changes were undone belongs to the undone turn but
                    // stays readable.
                    const dim =
                      turn?.undoneAt &&
                      !(item.kind === "marker" && item.marker?.kind === "undo")
                    if (item.id === pinned?.id) return null
                    return (
                      <Fragment key={item.id}>
                        <MessageScrollerItem
                          messageId={item.id}
                          scrollAnchor={item.id === lastUser}
                          className={cn(dim && "opacity-50")}
                          title={turn?.undoneAt ? "Undone" : undefined}
                        >
                          <ChatItemView
                            item={item}
                            ctx={{
                              env,
                              chatId: chat.id,
                              teammateName,
                              live: item.id === liveId,
                            }}
                          />
                        </MessageScrollerItem>
                        {footer && (
                          <MessageScrollerItem
                            className={cn(footer.undoneAt && "opacity-50")}
                          >
                            <TurnFooter
                              env={env}
                              chat={chat}
                              turn={footer}
                              turns={turns}
                              label={label}
                              onShowChanges={onShowChanges}
                            />
                          </MessageScrollerItem>
                        )}
                      </Fragment>
                    )
                  })}
                  {sending && (
                    <MessageScrollerItem key={sending.key} scrollAnchor>
                      <SendingMessage prompt={sending.prompt} />
                    </MessageScrollerItem>
                  )}
                  {running && !liveId && !sending && (
                    <MessageScrollerItem>
                      <p className="shimmer text-xs text-muted-foreground">
                        {teammateName} is starting…
                      </p>
                    </MessageScrollerItem>
                  )}
                </MessageScrollerContent>
              </MessageScrollerViewport>
              <MessageScrollerButton />
            </MessageScroller>
            <ComposerDock className="grid max-w-3xl gap-2">
              {pinned?.plan && (
                <PlanCard entries={pinned.plan.entries ?? []} live />
              )}
              <ChatComposer
                env={env}
                chat={chat}
                queued={queued}
                teammateName={teammateName}
                onSending={onSending}
                onSent={() => setSending((s) => s && { ...s, sent: true })}
              />
            </ComposerDock>
          </div>
        </MessageScrollerProvider>
      </OpenWorkspaceFile.Provider>
    </PreviewSandbox.Provider>
  )
}

const day = new Intl.DateTimeFormat(undefined, {
  month: "short",
  day: "numeric",
})

/**
 * The row above a long chat's first loaded turn. It loads the turns before it when clicked, and
 * when it scrolls into view after the thread opened at its end.
 */
function LoadEarlier({
  turns,
  loading,
  onLoad,
}: {
  turns: ChatTurn[]
  loading: boolean
  onLoad: () => Promise<void>
}) {
  const ref = useRef<HTMLDivElement>(null)
  const load = () => {
    const viewport = ref.current?.closest<HTMLElement>(
      '[aria-label="Messages"]'
    )
    if (viewport) holdPlace(viewport)
    return onLoad().catch((err: unknown) => toast.error(errorMessage(err)))
  }
  const loadRef = useRef(load)
  loadRef.current = load
  useEffect(() => {
    const el = ref.current
    const viewport = el?.closest<HTMLElement>('[aria-label="Messages"]')
    if (!el || !viewport) return
    // The thread opens at its end; until the reader scrolls, the row only passes by.
    let scrolled = false
    const onScroll = () => {
      scrolled = true
    }
    viewport.addEventListener("wheel", onScroll, { passive: true })
    viewport.addEventListener("touchmove", onScroll, { passive: true })
    viewport.addEventListener("keydown", onScroll)
    const seen = new IntersectionObserver(
      (entries) => {
        if (scrolled && entries.some((e) => e.isIntersecting))
          void loadRef.current()
      },
      { root: viewport, rootMargin: "200px 0px 0px 0px" }
    )
    seen.observe(el)
    return () => {
      seen.disconnect()
      viewport.removeEventListener("wheel", onScroll)
      viewport.removeEventListener("touchmove", onScroll)
      viewport.removeEventListener("keydown", onScroll)
    }
  }, [])
  const first = turns.at(0)
  const last = turns.at(-1)
  const range =
    first && last
      ? [
          ...new Set([
            day.format(new Date(first.startedAt)),
            day.format(new Date(last.startedAt)),
          ]),
        ].join(" – ")
      : ""
  return (
    <div ref={ref} className="grid justify-items-center gap-1.5 py-1">
      <div className="flex w-full items-center gap-3">
        <span className="h-px flex-1 bg-border" />
        <Button
          variant="outline"
          size="sm"
          disabled={loading}
          onClick={() => void load()}
        >
          {loading ? <Spinner /> : <ArrowUpIcon />}
          Load earlier · {turns.length} more{" "}
          {turns.length === 1 ? "turn" : "turns"}
        </Button>
        <span className="h-px flex-1 bg-border" />
      </div>
      <p className="font-mono text-xs text-muted-foreground">
        Scrolling up to this line loads them too · {range}
      </p>
    </div>
  )
}

/**
 * Keeps the first message in view where it is for a few seconds, while the turns loaded above
 * it render: being content-visibility auto, they start at a guessed height and settle as they
 * come near. The scroller keeps the place only when they arrive. Scrolling ends the hold.
 */
function holdPlace(viewport: HTMLElement) {
  const box = viewport.getBoundingClientRect()
  const el = [
    ...viewport.querySelectorAll<HTMLElement>("[data-message-id]"),
  ].find((m) => {
    const r = m.getBoundingClientRect()
    return r.bottom > box.top && r.top < box.bottom
  })
  const content = el?.parentElement
  if (!el || !content) return
  const top = () =>
    el.getBoundingClientRect().top - viewport.getBoundingClientRect().top
  const at = top()
  const intents = ["wheel", "touchmove", "keydown", "pointerdown"]
  const stop = () => {
    keep.disconnect()
    clearTimeout(timer)
    for (const e of intents) viewport.removeEventListener(e, stop)
  }
  const keep = new ResizeObserver(() => {
    if (!el.isConnected) return stop()
    const moved = top() - at
    if (Math.abs(moved) > 0.5) viewport.scrollTop += moved
  })
  keep.observe(content)
  for (const e of intents) viewport.addEventListener(e, stop, { passive: true })
  const timer = setTimeout(stop, 3000)
}

/**
 * The composer's place under a message list. It keeps the list's scrollbar gutter, so the two
 * line up on the right, and ends where the computer's screen does.
 */
export function ComposerDock({
  className,
  children,
}: {
  className?: string
  children: ReactNode
}) {
  return (
    <div className="shrink-0 scrollbar-thin scrollbar-gutter-stable overflow-hidden">
      <div className={cn("mx-auto w-full px-4 pb-2.5", className)}>
        {children}
      </div>
    </div>
  )
}

/** A message on its way to the chat, and how much of the log there was when it left. */
type Sending = {
  prompt: Prompt
  users: number
  queued: number
  key: number
  sent?: boolean
}

function ChatComposer({
  env,
  chat,
  queued,
  teammateName,
  onSending,
  onSent,
}: {
  env: string
  chat: Chat
  queued: QueuedPrompt[]
  teammateName: string
  /** The message being sent, or undefined when it didn't go. */
  onSending: (p: Prompt | undefined) => void
  onSent: () => void
}) {
  const [error, setError] = useState<string>()
  const [insert, setInsert] = useState<{ id: string; text: string }>()
  const path = { env, chat: chat.id }
  const sandbox = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes/{id}",
    { params: { path: { env, id: chat.sandboxId } } },
    { refetchInterval: 5000 }
  )

  if (chat.state === "terminal") return <InTerminal env={env} chat={chat} />
  if (chat.state === "orphaned") {
    return (
      <div className="grid justify-items-center gap-2 rounded-lg border border-dashed px-3 py-2.5 text-center text-xs text-muted-foreground">
        This chat ended: its sandbox was reset or removed, so the agent can't
        pick it up again.
        <Button
          size="sm"
          render={
            <Link
              to="/e/$env/chats/new"
              params={{ env }}
              search={{ sandbox: chat.sandboxId }}
            />
          }
        >
          Start a new chat
        </Button>
      </div>
    )
  }

  // While a turn runs, a message waits for it to end, after the ones already waiting.
  const send = async (prompt: Prompt) => {
    setError(undefined)
    onSending(prompt)
    try {
      const res = await fetchClient.POST(
        "/api/environments/{env}/chats/{chat}/queue",
        { params: { path }, body: prompt }
      )
      if (res.error) throw res.error
    } catch (err) {
      onSending(undefined)
      setError(errorMessage(err))
      return false
    }
    onSent()
    return true
  }
  const stop = () =>
    void fetchClient
      .POST("/api/environments/{env}/chats/{chat}/cancel", {
        params: { path },
      })
      .then((res) => {
        if (res.error) toast.error(errorMessage(res.error))
      })

  return (
    <>
      <SandboxStateBanner env={env} sandbox={sandbox.data} />
      <QueuedPrompts
        env={env}
        chatId={chat.id}
        queued={queued}
        teammateName={teammateName}
        onEdit={(q) => setInsert({ id: q.id, text: q.text })}
      />
      <Composer
        onSend={send}
        onStop={stop}
        running={chat.state === "running"}
        error={error}
        autoFocus
        insert={insert}
        mentionsIn={{ env, sandbox: chat.sandboxId }}
        placeholder={
          chat.state === "running"
            ? `Steer ${teammateName}: this goes once the current turn ends`
            : "Reply…"
        }
      />
    </>
  )
}

/** The chat continues in a terminal session; the composer waits until it comes back. */
function InTerminal({ env, chat }: { env: string; chat: Chat }) {
  const [busy, setBusy] = useState(false)
  const back = async () => {
    setBusy(true)
    try {
      const { error } = await fetchClient.POST(
        "/api/environments/{env}/chats/{chat}/return",
        { params: { path: { env, chat: chat.id } } }
      )
      if (error) throw error
    } catch (err) {
      toast.error(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="grid justify-items-center gap-2 rounded-lg border border-dashed px-3 py-2.5 text-center text-xs text-muted-foreground">
      This chat continues in the terminal. Bring it back here to keep chatting;
      what happened there shows up in this chat.
      <div className="flex gap-1.5">
        <Button
          size="sm"
          variant="outline"
          render={
            <Link
              to="/e/$env/teammates/$id"
              params={{ env, id: chat.teammateId }}
              search={{ thread: chat.id, tab: "terminal" }}
            />
          }
        >
          <TerminalWindowIcon />
          Go to the terminal
        </Button>
        <Button size="sm" disabled={busy} onClick={() => void back()}>
          {busy && <Spinner />}
          Back to chat
        </Button>
      </div>
    </div>
  )
}
