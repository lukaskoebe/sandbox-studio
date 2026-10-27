import { useState } from "react"
import { ArrowCounterClockwiseIcon, GitDiffIcon } from "@phosphor-icons/react"
import { toast } from "sonner"
import { turnUsage } from "@/components/chat/chat-items"
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
import { Spinner } from "@/components/ui/spinner"
import { errorMessage, fetchClient } from "@/lib/api/client"
import { changeCount, type Chat, type ChatTurn } from "@/lib/chats"

/**
 * The line under a finished request: the files it changed, Undo, and the usage the agent
 * reported. Nothing when there's nothing to say.
 */
export function TurnFooter({
  env,
  chat,
  turn,
  turns,
  label,
  onShowChanges,
}: {
  env: string
  chat: Chat
  turn: ChatTurn
  turns: ChatTurn[]
  label: (t: ChatTurn) => string
  onShowChanges: (turnId: string) => void
}) {
  const count = changeCount(turn)
  const usage = turnUsage(turn)
  if (!count && !usage) return null
  return (
    <div className="flex flex-wrap items-center gap-x-1 gap-y-0.5 text-caption text-muted-foreground">
      {count > 0 && (
        <Button
          variant="ghost"
          size="xs"
          className="-ml-2 text-muted-foreground"
          onClick={() => onShowChanges(turn.id)}
        >
          <GitDiffIcon />
          Changed {count} {count === 1 ? "file" : "files"}
        </Button>
      )}
      {count > 0 && turn.undoneAt && <span>· Undone</span>}
      {count > 0 && turn.undoable && (
        <UndoTurn
          env={env}
          chat={chat}
          turn={turn}
          later={turns.filter((t) => t.seq > turn.seq && !t.undoneAt).length}
          prompt={label(turn)}
        />
      )}
      {usage && <span className={count > 0 ? "ml-auto" : ""}>{usage}</span>}
    </div>
  )
}

function UndoTurn({
  env,
  chat,
  turn,
  later,
  prompt,
}: {
  env: string
  chat: Chat
  turn: ChatTurn
  /** Requests after this one that aren't undone yet: they're undone too. */
  later: number
  prompt: string
}) {
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const idle = chat.state === "idle"
  const title =
    later === 0
      ? "Undo this change?"
      : `Undo this and ${later} later ${later === 1 ? "change" : "changes"}?`

  const undo = async () => {
    setBusy(true)
    try {
      const { error } = await fetchClient.POST(
        "/api/environments/{env}/chats/{chat}/turns/{turn}/undo",
        { params: { path: { env, chat: chat.id, turn: turn.id } } }
      )
      if (error) throw error
      setOpen(false)
    } catch (err) {
      toast.error("Could not undo", { description: errorMessage(err) })
    } finally {
      setBusy(false)
    }
  }

  return (
    <AlertDialog open={open} onOpenChange={setOpen}>
      <AlertDialogTrigger
        render={
          <Button
            variant="ghost"
            size="xs"
            className="text-muted-foreground"
            disabled={!idle}
            title={
              idle
                ? "Put the files back as they were before this message"
                : chat.state === "running"
                  ? "Wait until your teammate is done, or stop it"
                  : "Bring the chat back from the terminal first"
            }
          />
        }
      >
        <ArrowCounterClockwiseIcon />
        Undo
      </AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          <AlertDialogDescription>
            The files in the sandbox go back to how they were before “{prompt}”.
            {later > 0 &&
              ` Everything changed after it is undone as well: ${
                later === 1
                  ? "your next message's changes"
                  : `the changes of your next ${later} messages`
              }.`}{" "}
            Git history and ignored files, such as installed packages, stay as
            they are. Your teammate is told on your next message.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction
            variant="destructive"
            disabled={busy || !idle}
            onClick={() => void undo()}
          >
            {busy && <Spinner />}
            Undo
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
