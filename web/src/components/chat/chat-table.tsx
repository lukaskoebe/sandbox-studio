import { Link, useNavigate } from "@tanstack/react-router"
import { ChatStateBadge } from "@/components/chat/chat-items"
import { TeammateAvatar } from "@/components/teammate-avatar"
import { Badge } from "@/components/ui/badge"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { $api } from "@/lib/api/client"
import type { Chat } from "@/lib/chats"
import { chatTitle } from "@/lib/chats"
import { formatAge } from "@/lib/utils"

/** Chats with their teammate, sandbox and state. A row opens the chat. */
export function ChatTable({
  env,
  chats,
  showTeammate = true,
}: {
  env: string
  chats: Chat[]
  showTeammate?: boolean
}) {
  const navigate = useNavigate()
  const teammates = $api.useQuery("get", "/api/environments/{env}/teammates", {
    params: { path: { env } },
  })
  const sandboxes = $api.useQuery("get", "/api/environments/{env}/sandboxes", {
    params: { path: { env } },
  })
  const teammate = (id: string) =>
    teammates.data?.find((t) => t.id === id)?.name ?? "Teammate"
  const avatar = (id: string) =>
    teammates.data?.find((t) => t.id === id)?.avatar
  const sandbox = (id: string) =>
    sandboxes.data?.find((s) => s.id === id)?.name ?? "removed sandbox"

  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Chat</TableHead>
          {showTeammate && <TableHead>Teammate</TableHead>}
          <TableHead>Sandbox</TableHead>
          <TableHead>Status</TableHead>
          <TableHead className="text-right">Last active</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {chats.map((c) => (
          <TableRow
            key={c.id}
            className="cursor-pointer"
            onClick={() =>
              void navigate({
                to: "/e/$env/chats/$chat",
                params: { env, chat: c.id },
              })
            }
          >
            <TableCell className="max-w-80 font-medium">
              <span className="flex min-w-0 items-center gap-2">
                <Link
                  to="/e/$env/chats/$chat"
                  params={{ env, chat: c.id }}
                  className="truncate outline-none focus-visible:underline"
                >
                  {chatTitle(c)}
                </Link>
                {c.heartbeat && (
                  <Badge variant="secondary" className="shrink-0">
                    Heartbeat
                  </Badge>
                )}
              </span>
            </TableCell>
            {showTeammate && (
              <TableCell>
                <span className="flex items-center gap-1.5">
                  <TeammateAvatar
                    name={teammate(c.teammateId)}
                    avatar={avatar(c.teammateId)}
                    className="size-4 text-[0.5rem]"
                  />
                  {teammate(c.teammateId)}
                </span>
              </TableCell>
            )}
            <TableCell className="text-muted-foreground">
              {sandbox(c.sandboxId)}
            </TableCell>
            <TableCell>
              <ChatStateBadge chat={c} />
            </TableCell>
            <TableCell className="text-right text-muted-foreground">
              {formatAge(c.updatedAt)}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}
