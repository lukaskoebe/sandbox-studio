import { useNavigate } from "@tanstack/react-router"
import { PlusIcon } from "@phosphor-icons/react"
import { TeammateAvatar } from "@/components/teammate-avatar"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { $api } from "@/lib/api/client"
import { lastUsedFirst, setNewChatOpen, useNewChatOpen } from "@/lib/new-chat"
import { newThreadWith } from "@/lib/new-thread"

/**
 * "Who should do it?": New chat picks the teammate first, the one used last on top. Picking
 * one opens its home with the composer focused, where the thread starts.
 */
export function NewChatDialog({
  env,
  onNewTeammate,
}: {
  env: string
  onNewTeammate: () => void
}) {
  const open = useNewChatOpen()
  const navigate = useNavigate()
  const teammates = $api.useQuery("get", "/api/environments/{env}/teammates", {
    params: { path: { env } },
  })
  const chats = $api.useQuery("get", "/api/environments/{env}/chats", {
    params: { path: { env } },
  })
  const list = lastUsedFirst(teammates.data ?? [], chats.data ?? [])
  const used = new Set(chats.data?.map((c) => c.teammateId))
  const pick = (id: string) => {
    setNewChatOpen(false)
    newThreadWith(id)
    void navigate({ to: "/e/$env/teammates/$id", params: { env, id } })
  }
  return (
    <Dialog open={open} onOpenChange={setNewChatOpen}>
      <DialogContent className="sm:max-w-sm">
        <DialogHeader>
          <DialogTitle>Who should do it?</DialogTitle>
          <DialogDescription>
            The thread starts on their page, with the box ready for your
            message.
          </DialogDescription>
        </DialogHeader>
        <div className="grid gap-1">
          {list.map((t, i) => (
            <Button
              key={t.id}
              variant="ghost"
              className="h-auto justify-start gap-2.5 py-1.5 text-left"
              onClick={() => pick(t.id)}
            >
              <TeammateAvatar name={t.name} avatar={t.avatar} />
              <span className="grid min-w-0 flex-1">
                <span className="truncate font-medium">{t.name}</span>
                {t.role && (
                  <span className="truncate text-caption font-normal text-muted-foreground">
                    {t.role}
                  </span>
                )}
              </span>
              {i === 0 && used.has(t.id) && list.length > 1 && (
                <span className="text-caption font-normal text-muted-foreground">
                  last used
                </span>
              )}
            </Button>
          ))}
          <Button
            variant="ghost"
            className="justify-start gap-2.5 text-muted-foreground"
            onClick={() => {
              setNewChatOpen(false)
              onNewTeammate()
            }}
          >
            <PlusIcon />
            Add a teammate…
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  )
}
