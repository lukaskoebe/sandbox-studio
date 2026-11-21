import { useLayoutEffect, useMemo, useRef } from "react"
import { WarningIcon } from "@phosphor-icons/react"
import { useChatActions } from "@/components/chat/chat-actions"
import { ChatThread } from "@/components/chat/chat-thread"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/spinner"
import { type Teammate } from "@/lib/api/client"
import { chatTitle, type Chat, type useChat } from "@/lib/chats"
import { usePublishThreadActions } from "@/lib/thread-actions"

/**
 * One thread with the teammate: the chat itself with its composer. Its title, state and options
 * are in the top bar's crumb, and its changes and terminal show on the screen beside it. The
 * sidebar and Esc go back to the teammate's home.
 */
export function TeammateThread({
  env,
  teammate,
  log,
  onBack,
  onShowChanges,
  onOpenFile,
  onTerminal,
}: {
  env: string
  teammate: Teammate
  log: ReturnType<typeof useChat>
  onBack: () => void
  onShowChanges: (turnId: string, file?: string) => void
  onOpenFile: (path: string) => void
  /** Shows the terminal session the thread was handed to. */
  onTerminal: (session: string) => void
}) {
  const chat = log.chat

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {/* The title shows in the top bar; the page keeps it as its heading for screen readers. */}
      {chat && <h1 className="sr-only">{chatTitle(chat)}</h1>}
      {chat && !log.gone && (
        <ThreadOptions
          env={env}
          chat={chat}
          onTerminal={onTerminal}
          onDeleted={onBack}
        />
      )}
      {chat && (log.status === "error" || !log.live) && (
        <div
          role="status"
          className="mx-4 mt-3 flex shrink-0 items-center gap-2 rounded-md bg-muted px-3 py-1 text-xs"
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
            onOpenFile={onOpenFile}
          />
        </div>
      )}
    </div>
  )
}

/**
 * The thread's options for its crumb in the top bar: rename it, copy its link, hand it to the
 * agent's terminal, delete it. Their dialogs stay here, with the thread.
 */
function ThreadOptions({
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
  const a = useChatActions(env, chat, { onTerminal, onDeleted })
  // The crumb calls the latest actions, and changes only when what it shows does.
  const latest = useRef(a)
  useLayoutEffect(() => {
    latest.current = a
  })
  const running = chat.state === "running"
  const actions = useMemo(
    () => ({
      chatId: chat.id,
      rename: () => latest.current.rename(),
      copyLink: () => latest.current.copyLink(),
      toTerminal: () => void latest.current.toTerminal(),
      remove: () => latest.current.remove(),
      handable: a.handable,
      handing: a.handing,
      running,
    }),
    [chat.id, a.handable, a.handing, running]
  )
  usePublishThreadActions(actions)
  return a.dialogs
}
