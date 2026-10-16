import { useState } from "react"
import { toast } from "sonner"
import {
  ClockIcon,
  PaperclipIcon,
  PencilSimpleIcon,
  XIcon,
} from "@phosphor-icons/react"
import { Button } from "@/components/ui/button"
import { errorMessage, errorStatus, fetchClient } from "@/lib/api/client"
import type { QueuedPrompt } from "@/lib/chats"

/**
 * The messages written while the agent works, waiting above the composer until the turn ends;
 * then they go one turn each, in order. Edit takes one back into the composer, Remove drops it.
 */
export function QueuedPrompts({
  env,
  chatId,
  queued,
  teammateName,
  onEdit,
}: {
  env: string
  chatId: string
  queued: QueuedPrompt[]
  teammateName: string
  onEdit: (q: QueuedPrompt) => void
}) {
  const [busy, setBusy] = useState<string>()
  if (queued.length === 0) return null

  const takeOff = async (q: QueuedPrompt, edit: boolean) => {
    setBusy(q.id)
    try {
      const { error } = await fetchClient.DELETE(
        "/api/environments/{env}/chats/{chat}/queue/{id}",
        { params: { path: { env, chat: chatId, id: q.id } } }
      )
      if (error) throw error
      if (edit) onEdit(q)
    } catch (err) {
      toast.error(
        errorStatus(err) === 404
          ? "That message already went."
          : errorMessage(err)
      )
    } finally {
      setBusy(undefined)
    }
  }

  return (
    <section aria-label="Queued messages" className="grid gap-1">
      <p className="flex items-center gap-1.5 px-1 text-[0.6875rem] text-muted-foreground">
        <ClockIcon className="size-3" aria-hidden />
        {queued.length === 1 ? "Queued" : `${queued.length} queued`}: sent when{" "}
        {teammateName} finishes this turn
      </p>
      <ul className="grid gap-1">
        {queued.map((q) => (
          <li
            key={q.id}
            className="flex items-start gap-2 rounded-lg border border-dashed bg-muted/30 py-1.5 pr-1 pl-3 text-sm"
          >
            <div className="min-w-0 flex-1 py-0.5">
              {q.text && (
                <p className="line-clamp-3 break-words whitespace-pre-wrap">
                  {q.text}
                </p>
              )}
              {q.attachments?.length ? (
                <p className="flex items-center gap-1 text-xs text-muted-foreground">
                  <PaperclipIcon className="size-3" aria-hidden />
                  <span className="truncate">{q.attachments.join(", ")}</span>
                </p>
              ) : null}
            </div>
            {/* Files can't go back into the composer, so only text-only messages are edited. */}
            {!q.attachments?.length && (
              <Button
                variant="ghost"
                size="icon-xs"
                title="Edit"
                aria-label="Edit queued message"
                disabled={busy === q.id}
                onClick={() => void takeOff(q, true)}
              >
                <PencilSimpleIcon />
              </Button>
            )}
            <Button
              variant="ghost"
              size="icon-xs"
              title="Remove"
              aria-label="Remove queued message"
              disabled={busy === q.id}
              onClick={() => void takeOff(q, false)}
            >
              <XIcon />
            </Button>
          </li>
        ))}
      </ul>
    </section>
  )
}
