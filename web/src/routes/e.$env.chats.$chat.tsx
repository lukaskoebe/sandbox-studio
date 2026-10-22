import { useState } from "react"
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  ArrowSquareOutIcon,
  CubeIcon,
  SidebarSimpleIcon,
  TrashIcon,
  WarningIcon,
} from "@phosphor-icons/react"
import { ChatStateBadge } from "@/components/chat/chat-items"
import { ChatThread, requestLabel } from "@/components/chat/chat-thread"
import {
  useWorkspacePanel,
  WorkspacePanel,
} from "@/components/chat/workspace-panel"
import { PageHeader } from "@/components/page-header"
import { phase, phaseDot } from "@/components/status-badge"
import { StatusDot } from "@/components/status-dot"
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
  EmptyTitle,
} from "@/components/ui/empty"
import { Spinner } from "@/components/ui/spinner"
import { $api, errorMessage, fetchClient } from "@/lib/api/client"
import { chatTitle, useChat, type Chat, type ChatTurn } from "@/lib/chats"

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
  const panel = useWorkspacePanel()
  const owner = teammates.data?.find((t) => t.id === chat?.teammateId)
  const sandbox = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes/{id}",
    { params: { path: { env, id: chat?.sandboxId ?? "" } } },
    { enabled: !!chat, refetchInterval: 5000 }
  )
  const teammateName = owner?.name ?? "Teammate"

  const label = (t: ChatTurn) => requestLabel(log.items, t)

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
        <TeammateAvatar
          name={teammateName}
          avatar={owner?.avatar}
          className="size-5 text-[0.5rem]"
        />
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
            <span className="max-sm:sr-only">Sandbox</span>
            {sandbox.data && (
              <StatusDot {...phaseDot(phase(sandbox.data))} compact />
            )}
          </Button>
          {(chat.state === "idle" || chat.state === "running") && (
            <OpenInTerminal env={env} chat={chat} />
          )}
          <DeleteChat env={env} chat={chat} />
          <Button
            variant="ghost"
            size="icon-sm"
            title={
              panel.open
                ? "Hide changes, files and preview"
                : "Show changes, files and preview"
            }
            aria-pressed={panel.open}
            onClick={() => panel.setOpen(!panel.open)}
          >
            <SidebarSimpleIcon className="-scale-x-100" />
          </Button>
        </div>
      </PageHeader>
      {(log.status === "error" || !log.live) && (
        <div
          role="status"
          className="flex shrink-0 items-center gap-2 border-b bg-muted px-3 py-1 text-xs"
        >
          <WarningIcon className="size-3 shrink-0 text-muted-foreground" />
          {log.error
            ? `${log.error} Trying again…`
            : "Reconnecting… new messages show up once Studio is back."}
        </div>
      )}
      <div className="flex min-h-0 flex-1">
        <ChatThread
          env={env}
          chat={chat}
          log={log}
          onShowChanges={panel.showChanges}
        />
        <WorkspacePanel
          env={env}
          chat={chat}
          turns={log.turns}
          label={label}
          panel={panel}
        />
      </div>
    </>
  )
}

/** Hands the chat to the agent's terminal in the sandbox's workspace. */
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
        to: "/e/$env/sandboxes/$id/work",
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
      <span className="max-sm:sr-only">Open in terminal</span>
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
