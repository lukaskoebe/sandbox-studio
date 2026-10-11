import { Fragment, useState } from "react"
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  ArrowSquareOutIcon,
  ChatCircleDotsIcon,
  CubeIcon,
  TerminalWindowIcon,
  TrashIcon,
  WarningIcon,
} from "@phosphor-icons/react"
import {
  ChatItemView,
  ChatStateBadge,
  turnUsage,
} from "@/components/chat/chat-items"
import { Composer, type Prompt } from "@/components/chat/composer"
import { PlanCard } from "@/components/chat/plan-card"
import { PageHeader } from "@/components/page-header"
import { TeammateAvatar } from "@/components/teammate-avatar"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog"
import { Button } from "@/components/ui/button"
import {
  Empty,
  EmptyContent,
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
import { chatTitle, useChat, type Chat, type ChatItem } from "@/lib/chats"
import { cn } from "@/lib/utils"

export const Route = createFileRoute("/e/$env/chats/$chat")({
  component: ChatPage,
})

function ChatPage() {
  const { env, chat: chatId } = Route.useParams()
  const log = useChat(env, chatId)
  const teammates = $api.useQuery("get", "/api/environments/{env}/teammates", {
    params: { path: { env } },
  })
  const chat = log.chat
  const teammateName =
    teammates.data?.find((t) => t.id === chat?.teammateId)?.name ?? "Teammate"

  if (log.gone) {
    return (
      <>
        <PageHeader>
          <h1 className="text-sm font-medium">Chat</h1>
        </PageHeader>
        <Empty>
          <EmptyHeader>
            <EmptyTitle>This chat was deleted</EmptyTitle>
            <EmptyDescription>
              Its messages are gone; the work it did stays in the sandbox.
            </EmptyDescription>
          </EmptyHeader>
          <EmptyContent>
            <Button
              size="sm"
              variant="outline"
              render={<Link to="/e/$env/chats" params={{ env }} />}
            >
              All chats
            </Button>
          </EmptyContent>
        </Empty>
      </>
    )
  }
  if (!chat) {
    return log.status === "error" ? (
      <Empty>
        <EmptyHeader>
          <EmptyTitle>Can't load this chat</EmptyTitle>
          <EmptyDescription>{log.error} Trying again…</EmptyDescription>
        </EmptyHeader>
      </Empty>
    ) : (
      <Spinner className="m-auto" />
    )
  }

  return (
    <>
      <PageHeader>
        <TeammateAvatar name={teammateName} className="size-5 text-[0.5rem]" />
        <h1
          className="min-w-0 truncate text-sm font-medium"
          title={chatTitle(chat)}
        >
          {chatTitle(chat)}
        </h1>
        <ChatStateBadge chat={chat} />
        <div className="ml-auto flex items-center gap-1">
          <Button
            variant="ghost"
            size="sm"
            title="The sandbox this chat works in"
            render={
              <Link
                to="/e/$env/sandboxes/$id"
                params={{ env, id: chat.sandboxId }}
              />
            }
          >
            <CubeIcon />
            Sandbox
          </Button>
          {(chat.state === "idle" || chat.state === "running") && (
            <OpenInTerminal env={env} chat={chat} />
          )}
          <DeleteChat env={env} chat={chat} />
        </div>
      </PageHeader>
      {(log.status === "error" || !log.live) && (
        <div
          role="status"
          className="flex shrink-0 items-center gap-2 border-b bg-amber-500/5 px-3 py-1 text-xs text-amber-700 dark:text-amber-400"
        >
          <WarningIcon className="size-3 shrink-0" />
          {log.error
            ? `${log.error} Trying again…`
            : "Reconnecting… new messages show up once Studio is back."}
        </div>
      )}
      <ChatLog
        env={env}
        chat={chat}
        items={log.items}
        turns={log.turns}
        teammateName={teammateName}
      />
    </>
  )
}

function ChatLog({
  env,
  chat,
  items,
  turns,
  teammateName,
}: {
  env: string
  chat: Chat
  items: ChatItem[]
  turns: ReturnType<typeof useChat>["turns"]
  teammateName: string
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
      <div className="flex min-h-0 flex-1 flex-col">
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
                const usage = lastOfTurn && turn?.endedAt ? turnUsage(turn) : ""
                if (item.id === pinned?.id) return null
                return (
                  <Fragment key={item.id}>
                    <MessageScrollerItem
                      messageId={item.id}
                      scrollAnchor={item.kind === "user"}
                      className={cn(turn?.undoneAt && "opacity-50")}
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
                    {usage && (
                      <MessageScrollerItem>
                        <p className="text-[0.6875rem] text-muted-foreground">
                          {usage}
                        </p>
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
          <ChatComposer env={env} chat={chat} />
        </div>
      </div>
    </MessageScrollerProvider>
  )
}

function ChatComposer({ env, chat }: { env: string; chat: Chat }) {
  const [error, setError] = useState<string>()
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

  const send = async (prompt: Prompt) => {
    setError(undefined)
    const res = await fetchClient.POST(
      "/api/environments/{env}/chats/{chat}/prompt",
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
    <Composer
      onSend={send}
      onStop={stop}
      running={chat.state === "running"}
      error={error}
      autoFocus
      placeholder={
        chat.state === "running"
          ? "Write your next message; send it once this one is done"
          : "Reply…"
      }
    />
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
              to="/e/$env/sandboxes/$id"
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

/** Hands the chat to the agent's terminal view in the sandbox (stage 4 of the design). */
function OpenInTerminal({ env, chat }: { env: string; chat: Chat }) {
  const navigate = useNavigate()
  const [busy, setBusy] = useState(false)
  const open = async () => {
    setBusy(true)
    try {
      const { data, error } = await fetchClient.POST(
        "/api/environments/{env}/chats/{chat}/terminal",
        { params: { path: { env, chat: chat.id } } }
      )
      if (error) throw error
      const name = data.session
      void navigate({
        to: "/e/$env/sandboxes/$id",
        params: { env, id: chat.sandboxId },
        search: { terminal: name },
      })
    } catch (err) {
      toast.error(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }
  return (
    <Button
      variant="ghost"
      size="sm"
      disabled={busy || chat.state === "running"}
      title={
        chat.state === "running"
          ? "Wait until the agent is done, or stop it"
          : "Continue this conversation in the agent's own terminal"
      }
      onClick={() => void open()}
    >
      {busy ? <Spinner /> : <ArrowSquareOutIcon />}
      Open in terminal
    </Button>
  )
}

function DeleteChat({ env, chat }: { env: string; chat: Chat }) {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const remove = $api.useMutation(
    "delete",
    "/api/environments/{env}/chats/{chat}",
    {
      onSuccess: () => {
        void queryClient.invalidateQueries({
          queryKey: ["get", "/api/environments/{env}/chats"],
        })
        void navigate({ to: "/e/$env/chats", params: { env } })
      },
      onError: (err) => toast.error(errorMessage(err)),
    }
  )
  return (
    <AlertDialog>
      <AlertDialogTrigger
        render={<Button variant="ghost" size="icon-sm" title="Delete chat" />}
      >
        <TrashIcon />
      </AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Delete {chatTitle(chat)}?</AlertDialogTitle>
          <AlertDialogDescription>
            The conversation is gone for good. What the agent changed in the
            sandbox stays.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction
            variant="destructive"
            disabled={remove.isPending}
            onClick={() =>
              remove.mutate({ params: { path: { env, chat: chat.id } } })
            }
          >
            {remove.isPending && <Spinner />}
            Delete
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
