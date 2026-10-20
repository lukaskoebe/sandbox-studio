import { Link } from "@tanstack/react-router"
import { ChatsIcon } from "@phosphor-icons/react"
import { CountPill } from "@/components/count-pill"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { $api } from "@/lib/api/client"
import { hasNews, useChatsSeen } from "@/lib/chat-seen"
import { chatStateLabels, chatTitle, type Chat } from "@/lib/chats"
import { cn, formatAge } from "@/lib/utils"

/** The chats of a sandbox, each opening in the workspace; a new one starts from its + menu. */
export function SandboxChats({
  env,
  id,
  onOpen,
}: {
  env: string
  id: string
  /** Opens a chat in the workspace. */
  onOpen: (chat: string) => void
}) {
  const chats = $api.useQuery(
    "get",
    "/api/environments/{env}/sandboxes/{id}/chats",
    { params: { path: { env, id } } }
  )
  const list = chats.data ?? []
  const seen = useChatsSeen()
  const news = list.filter((c) => hasNews(c, seen)).length
  return (
    <>
      {list.length > 0 && (
        <DropdownMenu>
          <DropdownMenuTrigger render={<Button variant="ghost" size="sm" />}>
            <ChatsIcon />
            <span className="max-md:sr-only">Chats</span>
            <CountPill
              count={news || list.length}
              news={news > 0}
              title={
                news > 0
                  ? `${news} new since you last looked`
                  : `${list.length} ${list.length === 1 ? "chat" : "chats"}`
              }
            />
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="w-72">
            <DropdownMenuGroup>
              <DropdownMenuLabel>Chats in this sandbox</DropdownMenuLabel>
              {list.slice(0, 10).map((c) => {
                const fresh = hasNews(c, seen)
                return (
                  <DropdownMenuItem key={c.id} onClick={() => onOpen(c.id)}>
                    <ChatDot chat={c} news={fresh} />
                    <span
                      className={cn(
                        "min-w-0 flex-1 truncate",
                        fresh && "font-medium"
                      )}
                    >
                      {chatTitle(c)}
                    </span>
                    <span
                      className={cn(
                        "shrink-0",
                        fresh ? "text-primary" : "text-muted-foreground"
                      )}
                      title={chatStateLabels[c.state]}
                    >
                      {c.state === "running"
                        ? "Working"
                        : fresh
                          ? "New"
                          : formatAge(c.updatedAt)}
                    </span>
                  </DropdownMenuItem>
                )
              })}
            </DropdownMenuGroup>
            {list.length > 10 && (
              <>
                <DropdownMenuSeparator />
                <DropdownMenuItem
                  render={<Link to="/e/$env/chats" params={{ env }} />}
                >
                  All chats
                </DropdownMenuItem>
              </>
            )}
          </DropdownMenuContent>
        </DropdownMenu>
      )}
    </>
  )
}

/**
 * A chat's mark in lists and tabs: a pulsing dot while its agent works, a green one when it
 * has news this browser hasn't seen, and nothing otherwise.
 */
export function ChatDot({ chat, news }: { chat: Chat; news: boolean }) {
  if (chat.state === "running")
    return (
      <span
        aria-label="Working"
        className="size-1.5 shrink-0 animate-pulse rounded-full bg-muted-foreground"
      />
    )
  if (news)
    return (
      <span
        aria-label="New"
        className="size-1.5 shrink-0 rounded-full bg-primary"
      />
    )
  return <span aria-hidden className="size-1.5 shrink-0" />
}
