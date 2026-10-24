import { useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  CaretLeftIcon,
  ChatCircleIcon,
  DotsThreeIcon,
  TerminalWindowIcon,
  TrashIcon,
  WarningIcon,
} from "@phosphor-icons/react"
import { ChatStateBadge } from "@/components/chat/chat-items"
import { ChatThread } from "@/components/chat/chat-thread"
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
} from "@/components/ui/alert-dialog"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { SidebarTrigger } from "@/components/ui/sidebar"
import { Spinner } from "@/components/ui/spinner"
import {
  $api,
  errorMessage,
  fetchClient,
  type Teammate,
} from "@/lib/api/client"
import { chatTitle, type Chat, type useChat } from "@/lib/chats"

/**
 * One thread with the teammate: the way back to its home, the thread's title and state, and the
 * chat itself with its composer. Its changes and terminal show on the screen beside it.
 */
export function TeammateThread({
  env,
  teammate,
  log,
  computer,
  onBack,
  onShowChanges,
  onTerminal,
}: {
  env: string
  teammate: Teammate
  log: ReturnType<typeof useChat>
  /** The computer it runs on, named when the teammate has more than one. */
  computer?: string
  onBack: () => void
  onShowChanges: (turnId: string) => void
  /** Shows the terminal session the thread was handed to. */
  onTerminal: (session: string) => void
}) {
  const chat = log.chat

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <header className="flex h-14 shrink-0 items-center gap-1.5 px-3 md:px-4">
        <SidebarTrigger className="md:hidden" />
        <Button
          variant="outline"
          size="sm"
          className="shrink-0 gap-1.5 pr-2.5 pl-1.5"
          onClick={onBack}
          title={`Back to ${teammate.name} (Esc)`}
        >
          <CaretLeftIcon />
          <TeammateAvatar
            name={teammate.name}
            avatar={teammate.avatar}
            className="size-4 text-[0.5rem]"
          />
          {teammate.name}
        </Button>
        {chat && (
          <>
            <ChatCircleIcon className="ml-1.5 size-4 shrink-0 text-muted-foreground" />
            <h1
              className="min-w-0 truncate text-sm font-semibold"
              title={chatTitle(chat)}
            >
              {chatTitle(chat)}
            </h1>
            {computer && (
              <span className="shrink-0 rounded-full border px-2 font-mono text-[0.6875rem] text-muted-foreground max-sm:hidden">
                on {computer}
              </span>
            )}
            <span className="ml-auto flex shrink-0 items-center gap-1">
              <ChatStateBadge chat={chat} />
              <ThreadMenu
                env={env}
                chat={chat}
                onTerminal={onTerminal}
                onDeleted={onBack}
              />
            </span>
          </>
        )}
      </header>
      {chat && (log.status === "error" || !log.live) && (
        <div
          role="status"
          className="mx-4 flex shrink-0 items-center gap-2 rounded-md bg-muted px-3 py-1 text-xs"
        >
          <WarningIcon className="size-3 shrink-0 text-muted-foreground" />
          {log.error
            ? `${log.error} Trying again…`
            : "Reconnecting… new messages show up once Studio is back."}
        </div>
      )}
      {log.gone ? (
        <div className="m-auto grid max-w-xs justify-items-center gap-3 text-center text-xs text-muted-foreground">
          <p className="text-sm font-medium text-foreground">
            This thread was deleted
          </p>
          Its messages are gone; the work it did stays on the computer.
          <Button size="sm" variant="outline" onClick={onBack}>
            Back to {teammate.name}
          </Button>
        </div>
      ) : !chat ? (
        log.status === "error" ? (
          <p role="alert" className="m-auto text-xs text-destructive">
            {log.error} Trying again…
          </p>
        ) : (
          <Spinner className="m-auto" />
        )
      ) : (
        <div className="flex min-h-0 flex-1">
          <ChatThread
            env={env}
            chat={chat}
            log={log}
            onShowChanges={onShowChanges}
          />
        </div>
      )}
    </div>
  )
}

/** The thread's options: hand it to the agent's terminal, or delete it. */
function ThreadMenu({
  env,
  chat,
  onTerminal,
  onDeleted,
}: {
  env: string
  chat: Chat
  onTerminal: (session: string) => void
  onDeleted: () => void
}) {
  const queryClient = useQueryClient()
  const [busy, setBusy] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const remove = $api.useMutation(
    "delete",
    "/api/environments/{env}/chats/{chat}",
    {
      onSuccess: () => {
        void queryClient.invalidateQueries({
          queryKey: ["get", "/api/environments/{env}/chats"],
        })
        setDeleting(false)
        onDeleted()
      },
      onError: (err) => toast.error(errorMessage(err)),
    }
  )
  const toTerminal = async () => {
    setBusy(true)
    try {
      const { data, error } = await fetchClient.POST(
        "/api/environments/{env}/chats/{chat}/terminal",
        { params: { path: { env, chat: chat.id } } }
      )
      if (error) throw error
      onTerminal(data.session)
    } catch (err) {
      toast.error(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }
  const handable = chat.state === "idle"

  return (
    <>
      <DropdownMenu modal={false}>
        <DropdownMenuTrigger
          render={
            <Button
              variant="ghost"
              size="icon-sm"
              aria-label="Thread options"
              title="Thread options"
            />
          }
        >
          {busy ? <Spinner /> : <DotsThreeIcon weight="bold" />}
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="w-64">
          <DropdownMenuItem
            disabled={!handable || busy}
            onClick={() => void toTerminal()}
          >
            <TerminalWindowIcon />
            <span className="grid">
              Continue in the terminal
              <span className="text-[0.6875rem] text-muted-foreground">
                {chat.state === "running"
                  ? "Once the agent is done, or stopped"
                  : "In the agent's own interface"}
              </span>
            </span>
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem
            variant="destructive"
            onClick={() => setDeleting(true)}
          >
            <TrashIcon />
            Delete thread
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
      <AlertDialog open={deleting} onOpenChange={setDeleting}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete {chatTitle(chat)}?</AlertDialogTitle>
            <AlertDialogDescription>
              The conversation is gone for good. What the agent changed on the
              computer stays.
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
    </>
  )
}
