import { createFileRoute, Link, useNavigate } from "@tanstack/react-router"
import { PlusIcon } from "@phosphor-icons/react"
import { ChatStateBadge } from "@/components/chat/chat-items"
import { PageHeader } from "@/components/page-header"
import { TeammateAvatar } from "@/components/teammate-avatar"
import { Button } from "@/components/ui/button"
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from "@/components/ui/empty"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Spinner } from "@/components/ui/spinner"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { $api, errorMessage } from "@/lib/api/client"
import { chatTitle } from "@/lib/chats"
import { formatAge } from "@/lib/utils"

export const Route = createFileRoute("/e/$env/chats/")({
  component: ChatsPage,
})

function ChatsPage() {
  const { env } = Route.useParams()
  const navigate = useNavigate()
  const chats = $api.useQuery("get", "/api/environments/{env}/chats", {
    params: { path: { env } },
  })
  const teammates = $api.useQuery("get", "/api/environments/{env}/teammates", {
    params: { path: { env } },
  })
  const sandboxes = $api.useQuery("get", "/api/environments/{env}/sandboxes", {
    params: { path: { env } },
  })
  const teammate = (id: string) =>
    teammates.data?.find((t) => t.id === id)?.name ?? "Teammate"
  const sandbox = (id: string) =>
    sandboxes.data?.find((s) => s.id === id)?.name ?? "removed sandbox"
  const list = chats.data ?? []

  return (
    <>
      <PageHeader>
        <h1 className="text-sm font-medium">Chats</h1>
        <Button
          size="sm"
          className="ml-auto"
          render={<Link to="/e/$env/chats/new" params={{ env }} />}
        >
          <PlusIcon />
          New chat
        </Button>
      </PageHeader>
      <ScrollArea
        className="min-h-0 flex-1"
        viewportClassName="flex flex-col gap-4 p-4"
      >
        <p className="max-w-3xl text-sm text-muted-foreground">
          Talk to a teammate about the work in one of its sandboxes; it asks you
          before anything risky.{" "}
          <a
            href="https://lukaskoebe.github.io/sandbox-studio/chat/"
            target="_blank"
            rel="noreferrer"
            className="underline underline-offset-4"
          >
            Learn more
          </a>
        </p>
        {chats.isPending ? (
          <Spinner />
        ) : chats.isError ? (
          <p role="alert" className="text-sm text-destructive">
            {errorMessage(chats.error)}
          </p>
        ) : list.length === 0 ? (
          <Empty>
            <EmptyHeader>
              <EmptyTitle>No chats yet</EmptyTitle>
              <EmptyDescription>
                Start one to ask a teammate for something.
              </EmptyDescription>
            </EmptyHeader>
            <Button
              size="sm"
              render={<Link to="/e/$env/chats/new" params={{ env }} />}
            >
              <PlusIcon />
              New chat
            </Button>
          </Empty>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Chat</TableHead>
                <TableHead>Teammate</TableHead>
                <TableHead>Sandbox</TableHead>
                <TableHead>Status</TableHead>
                <TableHead className="text-right">Last active</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {list.map((c) => (
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
                  <TableCell className="max-w-80 truncate font-medium">
                    <Link
                      to="/e/$env/chats/$chat"
                      params={{ env, chat: c.id }}
                      className="outline-none focus-visible:underline"
                    >
                      {chatTitle(c)}
                    </Link>
                  </TableCell>
                  <TableCell>
                    <span className="flex items-center gap-1.5">
                      <TeammateAvatar
                        name={teammate(c.teammateId)}
                        className="size-4 text-[0.5rem]"
                      />
                      {teammate(c.teammateId)}
                    </span>
                  </TableCell>
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
        )}
      </ScrollArea>
    </>
  )
}
