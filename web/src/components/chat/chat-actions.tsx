import { useState, type ReactElement, type ReactNode } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  LinkIcon,
  PencilSimpleIcon,
  TerminalWindowIcon,
  TrashIcon,
} from "@phosphor-icons/react"
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
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuTrigger,
} from "@/components/ui/context-menu"
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Spinner } from "@/components/ui/spinner"
import { $api, errorMessage, fetchClient } from "@/lib/api/client"
import { chatTitle, type Chat } from "@/lib/chats"

type Options = {
  /** After the chat is deleted, such as to leave its page. */
  onDeleted?: () => void
  /** Opens the agent's terminal session the chat was handed to. */
  onTerminal?: (session: string) => void
}

/**
 * What can be done to a chat from its menus: rename it, copy its link, hand it to the
 * terminal, delete it. The dialogs go next to the menu that opens them.
 */
export function useChatActions(env: string, chat: Chat, opts: Options = {}) {
  const queryClient = useQueryClient()
  const [renaming, setRenaming] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const [handing, setHanding] = useState(false)
  const refresh = () =>
    Promise.all([
      queryClient.invalidateQueries({
        queryKey: ["get", "/api/environments/{env}/chats"],
      }),
      queryClient.invalidateQueries({
        queryKey: ["get", "/api/environments/{env}/sandboxes/{id}/chats"],
      }),
    ])
  const remove = $api.useMutation(
    "delete",
    "/api/environments/{env}/chats/{chat}",
    {
      onSuccess: () => {
        void refresh()
        setDeleting(false)
        opts.onDeleted?.()
      },
      onError: (err) => toast.error(errorMessage(err)),
    }
  )
  const toTerminal = async () => {
    setHanding(true)
    try {
      const { data, error } = await fetchClient.POST(
        "/api/environments/{env}/chats/{chat}/terminal",
        { params: { path: { env, chat: chat.id } } }
      )
      if (error) throw error
      opts.onTerminal?.(data.session)
    } catch (err) {
      toast.error(errorMessage(err))
    } finally {
      setHanding(false)
    }
  }
  const copyLink = () => {
    const url = new URL(window.location.href)
    url.pathname = `/e/${env}/chats/${chat.id}`
    url.search = ""
    url.hash = ""
    navigator.clipboard.writeText(url.toString()).then(
      () => toast.success("Link copied"),
      () => toast.error("Couldn't copy the link")
    )
  }

  const dialogs = (
    <>
      <RenameChatDialog
        env={env}
        chat={chat}
        open={renaming}
        onOpenChange={setRenaming}
        onRenamed={() => void refresh()}
      />
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

  return {
    rename: () => setRenaming(true),
    remove: () => setDeleting(true),
    copyLink,
    toTerminal,
    handing,
    /** The terminal takes a chat only between turns. */
    handable: chat.state === "idle",
    terminal: opts.onTerminal !== undefined,
    dialogs,
  }
}

/** A chat's menu at the pointer: right-click its row for it. */
export function ChatContextMenu({
  env,
  chat,
  children,
  ...opts
}: Options & {
  env: string
  chat: Chat
  /** The row; it takes the menu's trigger props. */
  children: ReactElement
}) {
  const a = useChatActions(env, chat, opts)
  return (
    <>
      <ContextMenu>
        <ContextMenuTrigger render={children} />
        <ContextMenuContent
          className="w-56"
          aria-label={`${chatTitle(chat)}: options`}
        >
          <ContextMenuItem onClick={a.rename}>
            <PencilSimpleIcon />
            Rename…
          </ContextMenuItem>
          <ContextMenuItem onClick={a.copyLink}>
            <LinkIcon />
            Copy link
          </ContextMenuItem>
          {a.terminal && (
            <ContextMenuItem
              disabled={!a.handable || a.handing}
              onClick={() => void a.toTerminal()}
            >
              <TerminalWindowIcon />
              Continue in the terminal
            </ContextMenuItem>
          )}
          <ContextMenuSeparator />
          <ContextMenuItem variant="destructive" onClick={a.remove}>
            <TrashIcon />
            Delete…
          </ContextMenuItem>
        </ContextMenuContent>
      </ContextMenu>
      {a.dialogs}
    </>
  )
}

/** Names a chat; the agent's own titles no longer replace the name. */
function RenameChatDialog({
  env,
  chat,
  open,
  onOpenChange,
  onRenamed,
}: {
  env: string
  chat: Chat
  open: boolean
  onOpenChange: (open: boolean) => void
  onRenamed: () => void
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-sm">
        {open && (
          <RenameForm
            env={env}
            chat={chat}
            onDone={() => {
              onRenamed()
              onOpenChange(false)
            }}
          >
            <DialogHeader>
              <DialogTitle>Rename thread</DialogTitle>
            </DialogHeader>
          </RenameForm>
        )}
      </DialogContent>
    </Dialog>
  )
}

function RenameForm({
  env,
  chat,
  onDone,
  children,
}: {
  env: string
  chat: Chat
  onDone: () => void
  children: ReactNode
}) {
  const [title, setTitle] = useState(chat.title)
  const rename = $api.useMutation(
    "put",
    "/api/environments/{env}/chats/{chat}/title",
    {
      onSuccess: onDone,
      onError: (err) => toast.error(errorMessage(err)),
    }
  )
  const value = title.trim()
  return (
    <form
      className="grid gap-4"
      onSubmit={(e) => {
        e.preventDefault()
        if (!value || value === chat.title) return onDone()
        rename.mutate({
          params: { path: { env, chat: chat.id } },
          body: { title: value },
        })
      }}
    >
      {children}
      <Input
        aria-label="Title"
        placeholder={chatTitle(chat)}
        value={title}
        maxLength={200}
        autoFocus
        onFocus={(e) => e.currentTarget.select()}
        onChange={(e) => setTitle(e.target.value)}
      />
      <DialogFooter>
        <Button type="submit" disabled={!value || rename.isPending}>
          {rename.isPending && <Spinner />}
          Rename
        </Button>
      </DialogFooter>
    </form>
  )
}
