import { Link } from "@tanstack/react-router"
import { ChatCircleDotsIcon } from "@phosphor-icons/react"
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
import { chatStateLabels, chatTitle } from "@/lib/chats"
import { formatAge } from "@/lib/utils"

/**
 * The chats of a sandbox next to its agent sessions, so chat and terminal are each one
 * click away: a new chat, or one of the sandbox's chats, opened in the workspace.
 */
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
  return (
    <>
      <Button
        variant="ghost"
        size="sm"
        title="Start a chat with this sandbox's teammate"
        render={
          <Link
            to="/e/$env/chats/new"
            params={{ env }}
            search={{ sandbox: id }}
          />
        }
      >
        <ChatCircleDotsIcon />
        New chat
      </Button>
      {list.length > 0 && (
        <DropdownMenu>
          <DropdownMenuTrigger render={<Button variant="ghost" size="sm" />}>
            Chats
            <span className="rounded-full bg-primary px-1.5 text-[0.625rem] text-primary-foreground">
              {list.length}
            </span>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="w-72">
            <DropdownMenuGroup>
              <DropdownMenuLabel>Chats in this sandbox</DropdownMenuLabel>
              {list.slice(0, 10).map((c) => (
                <DropdownMenuItem key={c.id} onClick={() => onOpen(c.id)}>
                  <span className="min-w-0 flex-1 truncate">
                    {chatTitle(c)}
                  </span>
                  <span
                    className="shrink-0 text-muted-foreground"
                    title={chatStateLabels[c.state]}
                  >
                    {c.state === "running" ? "Working" : formatAge(c.updatedAt)}
                  </span>
                </DropdownMenuItem>
              ))}
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
