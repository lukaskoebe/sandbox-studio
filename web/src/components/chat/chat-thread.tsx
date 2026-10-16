import { Fragment, useState } from "react"
import { Link } from "@tanstack/react-router"
import { toast } from "sonner"
import { ChatCircleDotsIcon, TerminalWindowIcon } from "@phosphor-icons/react"
import { ChatItemView, turnUsage } from "@/components/chat/chat-items"
import { Composer, type Prompt } from "@/components/chat/composer"
import { PlanCard } from "@/components/chat/plan-card"
import { QueuedPrompts } from "@/components/chat/queued-prompts"
import { TurnFooter } from "@/components/chat/turn-footer"
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
import { cn } from "@/lib/utils"

/** A request's name in lists and dialogs: the start of its message. */
export function requestLabel(items: ChatItem[], t: ChatTurn) {
  const text = items
    .find((i) => i.turnId === t.id && i.kind === "user")
    ?.user?.text.trim()
  if (!text) return t.origin === "terminal" ? "In the terminal" : "A message"
  return text.length > 60 ? `${text.slice(0, 60)}…` : text
}

/**
 * A chat's messages and composer, from the log of useChat. The chat page and the sandbox
 * workspace show the same thread.
 */
export function ChatThread({
  env,
  chat,
  log,
  onShowChanges,
}: {
  env: string
  chat: Chat
  log: ReturnType<typeof useChat>
  onShowChanges: (turnId: string) => void
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
}: {
  env: string
  chat: Chat
  items: ChatItem[]
  turns: ChatTurn[]
  queued: QueuedPrompt[]
  teammateName: string
  label: (t: ChatTurn) => string
  onShowChanges: (turnId: string) => void
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

  return (
    <MessageScrollerProvider defaultScrollPosition="end" autoScroll>
      <div className="flex min-h-0 min-w-0 flex-1 flex-col">
        <MessageScroller className="min-h-0 flex-1">
          <MessageScrollerViewport aria-label="Messages">
            <MessageScrollerContent className="mx-auto w-full max-w-3xl gap-4 p-4">
              {items.length === 0 && (
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
                      scrollAnchor={item.kind === "user"}
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
              {running && !liveId && (
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
        <div className="mx-auto grid w-full max-w-3xl shrink-0 gap-2 px-4 pb-4">
          {pinned?.plan && (
            <PlanCard entries={pinned.plan.entries ?? []} live />
          )}
          <ChatComposer
            env={env}
            chat={chat}
            queued={queued}
            teammateName={teammateName}
          />
        </div>
      </div>
    </MessageScrollerProvider>
  )
}

function ChatComposer({
  env,
  chat,
  queued,
  teammateName,
}: {
  env: string
  chat: Chat
  queued: QueuedPrompt[]
  teammateName: string
}) {
  const [error, setError] = useState<string>()
  const [insert, setInsert] = useState<{ id: string; text: string }>()
  const path = { env, chat: chat.id }

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
    const res = await fetchClient.POST(
      "/api/environments/{env}/chats/{chat}/queue",
      { params: { path }, body: prompt }
    )
    if (res.error) {
      setError(errorMessage(res.error))
      return false
    }
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
              to="/e/$env/sandboxes/$id/work"
              params={{ env, id: chat.sandboxId }}
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
